package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

// These original native outputs and HTTP traces were captured before a GitHub
// plugin implementation existed. Expectations never execute candidate code.
func TestGitHubNativeReadingMatchesOriginal27Cases(t *testing.T) {
	raw, err := os.ReadFile("testdata/native-reader-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != "46c84a57d3ecd88979819d4626c98be705d5820458a2714a51c99ef944cd9895" {
		t.Fatal("original native oracle changed; do not regenerate it from a candidate")
	}
	var cases []struct {
		Name string `json:"name"`
		HTTP []struct {
			Path   string `json:"path"`
			Status int    `json:"status"`
			Body   string `json:"body"`
		} `json:"http"`
		Batch Batch  `json:"batch"`
		Error string `json:"error"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil || len(cases) != 27 {
		t.Fatal("incomplete native oracle", err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			cursor := 0
			client := peerClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer synthetic-test-token" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
					t.Error("changed original scoped authenticated request")
				}
				if cursor >= len(test.HTTP) {
					t.Errorf("unexpected additional source read %s", r.URL.RequestURI())
					http.Error(w, "unexpected source", http.StatusNotFound)
					return
				}
				expected := test.HTTP[cursor]
				cursor++
				if r.URL.RequestURI() != expected.Path {
					t.Errorf("source order/page changed: got %s want %s", r.URL.RequestURI(), expected.Path)
				}
				body := expected.Body
				switch body {
				case "<4194305 spaces>":
					body = strings.Repeat(" ", (4<<20)+1)
				case "<hex:5bff5d>":
					body = "[\xff]"
				}
				// Original readers must construct their own bounded page URLs and
				// refuse redirects, including these poisoned peer headers.
				w.Header().Set("Link", `<https://attacker.invalid/collect>; rel="next"`)
				w.Header().Set("Location", "https://attacker.invalid/collect")
				w.WriteHeader(expected.Status)
				_, _ = fmt.Fprint(w, body)
			})
			actual, err := client.readLegacyV1(context.Background(), testConfig())
			code := ""
			if err != nil {
				var failure *Failure
				if !errors.As(err, &failure) {
					t.Fatal(err)
				}
				code = failure.Code
			}
			got, marshalErr := json.Marshal(actual)
			want, expectedErr := json.Marshal(test.Batch)
			if marshalErr != nil || expectedErr != nil || !bytes.Equal(got, want) || code != test.Error || cursor != len(test.HTTP) {
				t.Fatalf("original output/identity/failure/complete trace changed: outputEqual=%t code=%q want=%q reads=%d/%d errors=%v/%v", bytes.Equal(got, want), code, test.Error, cursor, len(test.HTTP), marshalErr, expectedErr)
			}
		})
	}
}
