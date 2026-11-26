package sqlite

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/media/speech"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func audioFixture(t *testing.T) (*Store, *Runs, rsi.Host, string, func([]byte, ...string) ([]byte, error)) {
	t.Helper()
	s, r, a := controlFixture(t, nil)
	controlMain(t, r, a, "audio", "create")
	host := installArtifactWorkbench(t, s, r)
	cli, source := runtimeFeatureHost(t), t.TempDir()
	run := func(input []byte, args ...string) ([]byte, error) {
		cmd := exec.Command(cli, args...)
		cmd.Env = append(os.Environ(), "STEWARD_DATA_ROOT="+s.DataRoot())
		cmd.Stdin = bytes.NewReader(input)
		return cmd.CombinedOutput()
	}
	buildTestBinary(t, "../../../cmd/plugins/audio-transcript", filepath.Join(source, "plugin"))
	raw, err := os.ReadFile("../../../plugins/audio-transcript/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if raw, err = run(nil, "plugin", "seal", source); err != nil {
		t.Fatal(string(raw), err)
	}
	return s, r, host, source, run
}
func recording(seconds int) []byte {
	b := make([]byte, 44+seconds*32000)
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
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	b[45] = 127
	return b
}
func TestAudioTranscriptManagedOwnerReadAndLateForget(t *testing.T) {
	ctx := context.Background()
	s, r, host, packageDir, run := audioFixture(t)
	wav := recording(11)
	raw, err := run(wav, "artifact", "save", "meeting.wav", "audio/wav")
	if err != nil {
		t.Fatal(string(raw), err)
	}
	var doc artifact.Descriptor
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	observed, err := os.ReadFile("../../../tests/fixtures/speech/whisper-1.9.2-jfk.json")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	local := func(_ context.Context, b []byte, model string) ([]byte, error) {
		calls++
		if !bytes.Equal(b, wav) || model != "model" {
			t.Fatal("lost authorized input")
		}
		return observed, nil
	}
	if _, err = r.transcribeAudio(ctx, host, doc.Ref, "model", local); err == nil || calls != 0 {
		t.Fatal("decoded before activation", err)
	}
	if raw, err = run(nil, "audio", "enable-plugin", packageDir); err != nil {
		t.Fatal(string(raw), err)
	}
	metadata, err := r.ReadEvidence(ctx, evidence.Request{Kind: "artifact", Ref: strings.TrimPrefix(doc.Ref, "artifact:")})
	if err != nil || metadata.Text != "" || metadata.LocalTranscriptionCommand != "audio transcribe-local" {
		t.Fatal(metadata, err)
	}
	got, err := r.transcribeAudio(ctx, host, doc.Ref, "model", local)
	if err != nil || calls != 1 || got.OriginalRef != doc.Ref || got.ContentHash != doc.ContentHash || got.OriginalName != doc.Name || got.DurationMS != 11000 || len(got.Segments) < 1 || got.Segments[0].StartMS != 0 || !strings.Contains(strings.ToLower(got.Segments[0].Text), "americans") {
		t.Fatal(got, err)
	}
	fail := func(context.Context, []byte, string) ([]byte, error) { return nil, speech.ErrInference }
	if got, err := r.transcribeAudio(ctx, host, doc.Ref, "model", fail); !errors.Is(err, speech.ErrInference) || len(got.Segments) != 0 {
		t.Fatal(got, err)
	}
	s.MainBoundary = func(point string) {
		if point == "before_local_audio_transcript" {
			if err := host.Store.Stop(ctx, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got, err := r.transcribeAudio(ctx, host, doc.Ref, "model", local); err == nil || got.OriginalRef != "" {
		t.Fatal("late Guardian stop returned transcript", got, err)
	}
	s.MainBoundary = nil
	if err := host.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	s.MainBoundary = func(point string) {
		if point == "before_local_audio_transcript" {
			_, e := s.db.Exec(`INSERT INTO memory_privacy(agent_id,epoch,updated_at) VALUES(?,1,?) ON CONFLICT(agent_id) DO UPDATE SET epoch=epoch+1,updated_at=excluded.updated_at`, mustOwner(t, s), time.Now().UnixMicro())
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	got, err = r.transcribeAudio(ctx, host, doc.Ref, "model", local)
	s.MainBoundary = nil
	if err == nil || got.OriginalRef != "" || len(got.Segments) > 0 {
		t.Fatal("delivered after Forget", got, err)
	}
	before := calls
	if _, err = r.transcribeAudio(ctx, host, doc.Ref, "model", local); err == nil || calls != before {
		t.Fatal("old audio decoded after Forget", err, calls)
	}
}
func TestAudioTranscriptOfficialLocalCLI(t *testing.T) {
	model, sample := os.Getenv("STEWARD_TEST_WHISPER_MODEL"), os.Getenv("STEWARD_TEST_WHISPER_WAV")
	if model == "" || sample == "" {
		t.Skip("set paths to the official tiny.en and JFK fixture")
	}
	_, _, _, packageDir, run := audioFixture(t)
	wav, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	check := func(input []byte, args ...string) []byte {
		t.Helper()
		raw, err := run(input, args...)
		if err != nil {
			t.Fatal(args, string(raw), err)
		}
		return raw
	}
	var doc artifact.Descriptor
	if err = json.Unmarshal(check(wav, "artifact", "save", "jfk.wav", "audio/wav"), &doc); err != nil {
		t.Fatal(err)
	}
	check(nil, "audio", "enable-plugin", packageDir)
	raw := check(nil, "audio", "transcribe-local", doc.Ref, model)
	var result AudioTranscript
	if json.Unmarshal(raw, &result) != nil || result.OriginalRef != doc.Ref || result.ContentHash != doc.ContentHash || result.ModelSHA256 != speech.ModelDigest || len(result.Segments) == 0 || result.Segments[0].StartMS != 0 || result.Segments[0].EndMS <= 0 || !strings.Contains(strings.ToLower(result.Segments[0].Text), "fellow americans") {
		t.Fatal(string(raw))
	}
	if !bytes.Equal(check(nil, "artifact", "open", result.OriginalRef), wav) {
		t.Fatal("original recording changed")
	}
	t.Log(string(raw))
}
