// Package speech runs the owner-selected local decoder in the existing OS sandbox.
package speech

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
)

const MaxAudioBytes = 20 << 20
const ModelDigest = "921e4cf8686fdd993dcd081a5da5b6c365bfde1162e72b08d75ac75289920b1f"

var (
	ErrFormat    = errors.New("supported audio is mono 16-bit PCM WAV at 16000 Hz, up to ten minutes")
	ErrSilence   = errors.New("no speech detected in recording")
	ErrModel     = errors.New("the official ggml-tiny.en model is missing or has a different digest")
	ErrEngine    = errors.New("whisper-cli 1.9.2 or the local OS sandbox is unavailable")
	ErrInference = errors.New("local transcription failed")
	ErrOutput    = errors.New("local recognizer produced incompatible output")
)

// Inspect standard RIFF chunks before handing verified file bytes to the decoder.
func Duration(wav []byte) (int64, error) {
	if len(wav) < 44 || len(wav) > MaxAudioBytes || string(wav[:4]) != "RIFF" || int64(binary.LittleEndian.Uint32(wav[4:8])) != int64(len(wav)-8) || string(wav[8:12]) != "WAVE" {
		return 0, ErrFormat
	}
	valid := false
	var samples []byte
	for pos := 12; pos < len(wav); {
		if len(wav)-pos < 8 {
			return 0, ErrFormat
		}
		size := int64(binary.LittleEndian.Uint32(wav[pos+4:]))
		end := int64(pos+8) + size
		if end > int64(len(wav)) {
			return 0, ErrFormat
		}
		chunk := wav[pos+8 : end]
		switch string(wav[pos : pos+4]) {
		case "fmt ":
			if valid || len(chunk) < 16 || binary.LittleEndian.Uint16(chunk) != 1 || binary.LittleEndian.Uint16(chunk[2:]) != 1 || binary.LittleEndian.Uint32(chunk[4:]) != 16000 || binary.LittleEndian.Uint32(chunk[8:]) != 32000 || binary.LittleEndian.Uint16(chunk[12:]) != 2 || binary.LittleEndian.Uint16(chunk[14:]) != 16 {
				return 0, ErrFormat
			}
			valid = true
		case "data":
			if samples != nil || !valid {
				return 0, ErrFormat
			}
			samples = chunk
		}
		pos = int(end + size%2)
		if pos > len(wav) {
			return 0, ErrFormat
		}
	}
	if !valid || len(samples) < 32 || len(samples)%2 != 0 || len(samples) > 600*32000 {
		return 0, ErrFormat
	}
	for i := 0; i < len(samples); i += 2 {
		if binary.LittleEndian.Uint16(samples[i:]) != 0 {
			return int64((len(samples) + 31) / 32), nil
		}
	}
	return 0, ErrSilence
}

func Run(ctx context.Context, wav []byte, model string) ([]byte, error) {
	exe, err := exec.LookPath("whisper-cli")
	if err != nil || sandbox.Check() != nil {
		return nil, ErrEngine
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil || !filepath.IsAbs(exe) {
		return nil, ErrEngine
	}
	root, err := os.MkdirTemp("", "steward-transcribe-")
	if err != nil {
		return nil, ErrInference
	}
	defer os.RemoveAll(root)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, ErrInference
	}
	source, err := os.OpenFile(model, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrModel
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != 77704715 {
		return nil, ErrModel
	}
	modelCopy := filepath.Join(root, "model.bin")
	f, err := os.OpenFile(modelCopy, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, ErrInference
	}
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, digest), io.LimitReader(source, info.Size()+1))
	closeErr := f.Close()
	if closeErr != nil || err != nil || n != info.Size() || hex.EncodeToString(digest.Sum(nil)) != ModelDigest {
		return nil, ErrModel
	}
	input := filepath.Join(root, "input.wav")
	if err = os.WriteFile(input, wav, 0600); err != nil {
		return nil, ErrInference
	}
	return runDecoder(ctx, exe, root)
}
func runDecoder(ctx context.Context, exe, root string) ([]byte, error) {
	modelCopy, input := filepath.Join(root, "model.bin"), filepath.Join(root, "input.wav")
	readRoots := []string{}
	if strings.HasPrefix(exe, "/opt/homebrew/") {
		readRoots = append(readRoots, "/opt/homebrew")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	prefix := filepath.Join(root, "output")
	// Bind the adapter to the official CLI output captured by the fixture.
	probe, err := sandbox.Command(ctx, exe, []string{"--version"}, readRoots, root, root)
	if err != nil {
		return nil, ErrEngine
	}
	env := []string{"LANG=C", "LC_ALL=C", "HOME=" + root, "TMPDIR=" + root, "PATH=/usr/bin:/bin"}
	probe.Env = env
	probe.Dir = root
	version := sandbox.NewOutput(8192)
	probe.Stdout = version
	probe.Stderr = version
	if _, err := sandbox.Run(probe, syscall.SIGKILL); err != nil || version.Overflow() || !strings.Contains("\n"+string(version.Bytes())+"\n", "\nwhisper.cpp version: 1.9.2\n") {
		return nil, ErrEngine
	}
	cmd, err := sandbox.Command(ctx, exe, []string{"-m", modelCopy, "-f", input, "-oj", "-of", prefix, "-l", "en", "-ng", "-np", "-t", "2"}, readRoots, root, root)
	if err != nil {
		return nil, ErrEngine
	}
	cmd.Env = env
	cmd.Dir = root
	if _, err = sandbox.Run(cmd, syscall.SIGKILL); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrInference
	}
	out, err := os.OpenFile(prefix+".json", os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrOutput
	}
	defer out.Close()
	info, err := out.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 1<<20 {
		return nil, ErrOutput
	}
	raw, err := io.ReadAll(io.LimitReader(out, (1<<20)+1))
	if err != nil || len(raw) == 0 || len(raw) > 1<<20 {
		return nil, ErrOutput
	}
	return raw, nil
}
