package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"strings"
	"unicode"

	"github.com/z-chenhao/assistant/internal/artifact"
	artifactsqlite "github.com/z-chenhao/assistant/internal/artifact/sqlite"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/media/speech"
	"github.com/z-chenhao/assistant/internal/plugin/audiotranscript/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type AudioSegment struct {
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
}
type AudioTranscript struct {
	OriginalRef    string         `json:"original_ref"`
	OriginalName   string         `json:"original_name"`
	ContentHash    string         `json:"content_hash"`
	ReaderRevision string         `json:"reader_revision"`
	ModelSHA256    string         `json:"model_sha256"`
	Language       string         `json:"language"`
	DurationMS     int64          `json:"duration_ms"`
	Segments       []AudioSegment `json:"segments"`
}

func (s *Store) EnableAudioTranscript(ctx context.Context, h rsi.Host, source string) (rsi.Status, error) {
	makeCase := func(raw, expected string) rsi.Case {
		return rsi.Case{InstanceID: "audio-transcript", Contract: contract.Service, Input: json.RawMessage(`{"operation":"segments","duration_ms":4000}`), Inputs: []rsi.InputData{{ID: "inference", Data: []byte(raw)}}, Expected: json.RawMessage(expected)}
	}
	cases := []rsi.Case{
		{InstanceID: "audio-transcript", Contract: contract.Service, Input: json.RawMessage(`{"operation":"plan","duration_ms":4000}`), Expected: json.RawMessage(`{"decoder":"whisper-tiny-en-cpu"}`)},
		makeCase(`{"transcription":[{"offsets":{"from":0,"to":2500},"text":" Review notes."}]}`, `{"segments":[{"start_ms":0,"end_ms":2500,"text":"Review notes."}]}`),
		makeCase(`{"transcription":[]}`, `{"error":"no_speech"}`),
		makeCase(`{"transcription":[{"text":"unbound"}]}`, `{"error":"incompatible_output"}`),
	}
	return s.enableOptionalReadPlugin(ctx, h, source, optionalReadPlugin{instance: "audio-transcript", pluginID: contract.PluginID, contract: contract.Service, goal: "Enable local timestamped audio transcripts", cases: cases})
}

func wavMediaType(media string) bool {
	switch media {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return true
	}
	return false
}
func (r *Runs) localTranscriptionEnabled(ctx context.Context, a evidenceAccess) (bool, error) {
	var enabled bool
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := r.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		var err error
		enabled, err = managedService(ctx, tx, contract.Service)
		return err
	})
	return enabled, err
}
func (r *Runs) TranscribeLocalAudio(ctx context.Context, h rsi.Host, ref, model string) (AudioTranscript, error) {
	return r.transcribeAudio(ctx, h, ref, model, speech.Run)
}

type localRecognizer func(context.Context, []byte, string) ([]byte, error)

func (r *Runs) transcribeAudio(ctx context.Context, h rsi.Host, ref, model string, recognize localRecognizer) (transcript AudioTranscript, resultErr error) {
	invalid := AudioTranscript{}
	if !strings.Contains(ref, ":") {
		ref = "artifact:" + ref
	}
	if !strings.HasPrefix(ref, "artifact:") || !evidence.ValidRef(ref) || model == "" {
		return invalid, evidence.ErrInvalid
	}
	status, err := h.Store.Status(ctx)
	if err != nil {
		return invalid, err
	}
	if status.Stopped || status.Revision == "" {
		return invalid, rsi.ErrRevoked
	}
	if _, err = h.ServiceInstance(ctx, status.Revision, contract.Service); err != nil {
		if errors.Is(err, rsi.ErrNotFound) {
			return invalid, errors.New("enable the audio-transcript plugin before transcribing")
		}
		return invalid, err
	}
	var a evidenceAccess
	var opened *artifactsqlite.Opened
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		var e error
		a, e = r.evidenceAccess(ctx, tx, nil)
		if e != nil {
			return e
		}
		d, e := r.host.Artifacts().DescribeTx(ctx, tx, a.scope, ref, true)
		if e != nil {
			return e
		}
		if d.CreatedAt.UnixMicro() <= a.floor {
			return artifact.ErrNotFound
		}
		media, _, _ := mime.ParseMediaType(d.MediaType)
		if !wavMediaType(media) {
			return speech.ErrFormat
		}
		if d.ByteSize > speech.MaxAudioBytes {
			return speech.ErrFormat
		}
		opened, e = r.host.Artifacts().OpenFileTx(ctx, tx, a.scope, ref)
		return e
	})
	if err != nil {
		return invalid, err
	}
	d := opened.Descriptor()
	f, err := opened.Verify(ctx)
	if err != nil {
		return invalid, err
	}
	defer f.Close()
	source, err := io.ReadAll(io.LimitReader(f, speech.MaxAudioBytes+1))
	if err != nil {
		return invalid, err
	}
	scope := r.host.Plugins()
	scope.Fence = func(ctx context.Context, tx *sql.Tx) error {
		now, err := r.evidenceAccess(ctx, tx, nil)
		if err != nil {
			return err
		}
		if now.scope != a.scope || now.epoch != a.epoch || now.floor != a.floor {
			return artifact.ErrNotFound
		}
		var graph string
		var stopped bool
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,''),stopped FROM rsi_control WHERE singleton=1`).Scan(&graph, &stopped); err != nil {
			return err
		}
		if stopped || graph != status.Revision {
			return rsi.ErrRevoked
		}
		current, err := r.host.Artifacts().DescribeTx(ctx, tx, now.scope, ref, true)
		if err != nil {
			return err
		}
		if current.Version != d.Version || current.ContentHash != d.ContentHash {
			return artifact.ErrNotFound
		}
		return nil
	}
	h.Store = scope
	if err = h.Store.CheckGraph(ctx, status.Revision); err != nil {
		return invalid, err
	}
	defer func() {
		if err := h.Store.CheckGraph(ctx, status.Revision); err != nil {
			transcript = AudioTranscript{}
			resultErr = err
		}
	}()
	duration, err := speech.Duration(source)
	if err != nil {
		return invalid, err
	}
	planRequest, _ := json.Marshal(contract.Request{Operation: "plan", DurationMS: duration})
	planRaw, err := h.ReadService(ctx, status.Revision, contract.Service, planRequest, nil)
	if err != nil {
		return invalid, err
	}
	var plan contract.Result
	if rsiplugin.DecodeResult(planRaw, &plan) != nil || plan.Decoder != "whisper-tiny-en-cpu" || plan.Error != "" || len(plan.Segments) > 0 {
		return invalid, speech.ErrOutput
	}
	raw, err := recognize(ctx, source, model)
	if err != nil {
		return invalid, err
	}
	request, _ := json.Marshal(contract.Request{Operation: "segments", DurationMS: duration})
	output, err := h.ReadService(ctx, status.Revision, contract.Service, request, []rsi.InputData{{ID: "inference", Data: raw}})
	if err != nil {
		return invalid, err
	}
	var result contract.Result
	if rsiplugin.DecodeResult(output, &result) != nil || result.Decoder != "" {
		return invalid, speech.ErrOutput
	}
	switch result.Error {
	case "no_speech":
		return invalid, speech.ErrSilence
	case "":
	default:
		return invalid, speech.ErrOutput
	}
	if len(result.Segments) == 0 || len(result.Segments) > 2048 {
		return invalid, speech.ErrOutput
	}
	segments := make([]AudioSegment, 0, len(result.Segments))
	var previous int64
	for _, seg := range result.Segments {
		if seg.StartMS < previous || seg.EndMS <= seg.StartMS || seg.EndMS > duration || strings.TrimSpace(seg.Text) == "" || len(seg.Text) > 8192 || strings.IndexFunc(seg.Text, unicode.IsControl) >= 0 {
			return invalid, speech.ErrOutput
		}
		segments = append(segments, AudioSegment{StartMS: seg.StartMS, EndMS: seg.EndMS, Text: seg.Text})
		previous = seg.EndMS
	}
	r.host.boundary("before_local_audio_transcript")
	return AudioTranscript{OriginalRef: ref, OriginalName: d.Name, ContentHash: d.ContentHash, ReaderRevision: status.Revision, ModelSHA256: speech.ModelDigest, Language: "en", DurationMS: duration, Segments: segments}, nil
}
