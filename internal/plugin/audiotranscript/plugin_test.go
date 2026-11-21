package audiotranscript

import "testing"

func TestTranscriptionRejectsMissingAndUnboundTimestamps(t *testing.T) {
	for _, raw := range []string{`{}`, `{"transcription":[{"text":"text"}]}`, `{"transcription":[{"offsets":{"from":0,"to":1100},"text":"text"}]}`, `{"transcription":[{"offsets":{"from":20,"to":500},"text":"first"},{"offsets":{"from":100,"to":600},"text":"overlap"}]}`} {
		if got := parse([]byte(raw), 1000); got.Error != "incompatible_output" || len(got.Segments) != 0 {
			t.Fatal(raw, got)
		}
	}
	if got := parse([]byte(`{"transcription":[]}`), 1000); got.Error != "no_speech" {
		t.Fatal(got)
	}
}
