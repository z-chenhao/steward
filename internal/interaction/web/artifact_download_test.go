package web

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/artifact"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type downloadCore struct {
	Core
	size   int64
	source io.Reader
}

func (c downloadCore) DescribeArtifact(context.Context, string) (artifact.Descriptor, error) {
	return artifact.Descriptor{Name: "report.txt", ByteSize: c.size}, nil
}
func (c downloadCore) OpenArtifact(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(c.source), nil
}

type downloadReadFailure struct{}

func (downloadReadFailure) Read([]byte) (int, error) {
	return 0, errors.New("fixture storage unavailable")
}
func TestArtifactDownloadRequiresAllDeclaredBytes(t *testing.T) {
	content := strings.Repeat("report line\n", 2048)
	for _, broken := range []bool{false, true} {
		var reader io.Reader = strings.NewReader(content)
		if broken {
			reader = io.MultiReader(strings.NewReader(content[:len(content)/2]), downloadReadFailure{})
		}
		server := Server{Core: downloadCore{size: int64(len(content)), source: reader}}
		peer := httptest.NewServer(http.HandlerFunc(server.get))
		response, err := peer.Client().Get(peer.URL + "/artifact?ref=artifact:test")
		if err != nil {
			peer.Close()
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		peer.Close()
		if response.ContentLength != int64(len(content)) {
			t.Fatal("missing expected size", response.ContentLength)
		}
		if broken {
			if readErr == nil || len(got) >= len(content) {
				t.Fatal("accepted truncated download", len(got), readErr)
			}
		} else if readErr != nil || string(got) != content {
			t.Fatal("complete download failed", len(got), readErr)
		}
	}
}
