package speech

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/computer/sandbox"
)

func TestSpeechWAVProfileAndSilence(t *testing.T) {
	b := make([]byte, 44+32000)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 32000)
	if _, err := Duration(b); !errors.Is(err, ErrSilence) {
		t.Fatal(err)
	}
	b[250] = 10
	if d, err := Duration(b); err != nil || d != 1000 {
		t.Fatal(d, err)
	}
	for _, pos := range []int{0, 4, 14, 16, 20, 22, 24, 28, 32, 34, 36, 40} {
		t.Run(string(rune('A'+pos)), func(t *testing.T) {
			copy := append([]byte(nil), b...)
			copy[pos] ^= 0xff
			if _, err := Duration(copy); !errors.Is(err, ErrFormat) {
				t.Fatal(pos, err)
			}
		})
	}
}

// The negative executables exercise the actual sandbox and command exit path.
// The positive protocol fixture is captured separately from the official binary.
func TestSpeechCommandFailureAndIncompatibleVersion(t *testing.T) {
	if sandbox.Check() != nil {
		t.Skip("OS sandbox required")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "whisper-cli")
	code := `package main
import("fmt";"os")
func main(){
	mode,err:=os.ReadFile("mode");if err!=nil{os.Exit(10)}
	if len(os.Args)==2 && os.Args[1]=="--version" { if string(mode)=="unsupported-version" {fmt.Println("whisper.cpp version: 1.8.0")}else{fmt.Println("whisper.cpp version: 1.9.2")};return }
	if string(mode)=="decode-exit"{os.Exit(2)}
}`
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", exe, src).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	exe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want error
	}{{"unsupported-version", ErrEngine}, {"decode-exit", ErrInference}, {"missing-output", ErrOutput}} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "mode"), []byte(tc.name), 0600); err != nil {
				t.Fatal(err)
			}
			_, err = runDecoder(context.Background(), exe, root)
			if err == nil || !errors.Is(err, tc.want) || strings.Contains(err.Error(), root) {
				t.Fatal(err)
			}
		})
	}
}
