package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"strings"

	"github.com/z-chenhao/assistant/internal/artifact"
	artifactsqlite "github.com/z-chenhao/assistant/internal/artifact/sqlite"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/media/docx"
	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	docxreader "github.com/z-chenhao/assistant/internal/plugin/docxreader/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const artifactSearchWindow = 512 << 10

func (t *tools) artifactBodyMatches(ctx context.Context, inv tool.Invocation, host rsi.Host, a evidenceAccess, c artifactsqlite.SearchCandidate, query string) (bool, error) {
	media, _, _ := mime.ParseMediaType(c.MediaType)
	switch {
	case media == docx.MediaType:
		managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), docxreader.Contract)
		if err != nil || !managed {
			return false, err
		}
		a.docxPlugin = true
	case strings.HasPrefix(media, "text/"), media == "application/json", strings.HasSuffix(media, "+json"):
	default:
		return false, nil
	}
	files := t.runs.host.Artifacts()
	// Authorize the original and the exact accepted invocation on every plugin
	// input callback, even if the file is withdrawn while a large window is read.
	store := t.runs.host.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error {
		if err := t.runs.evidenceFence(ctx, tx, a); err != nil {
			return err
		}
		if _, _, _, err := t.acceptedBuiltin(ctx, tx, inv); err != nil {
			return err
		}
		d, err := files.DescribeTx(ctx, tx, a.scope, c.Ref, true)
		if err != nil {
			return err
		}
		if d.Version != c.Version || d.ContentHash != c.ContentHash {
			return artifact.ErrNotFound
		}
		return nil
	}
	host.Store = store
	var opened *artifactsqlite.Opened
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := store.Fence(ctx, tx); err != nil {
			return err
		}
		var err error
		opened, err = files.OpenFileTx(ctx, tx, a.scope, c.Ref)
		return err
	})
	if err != nil {
		return false, err
	}
	source, err := opened.Verify(ctx)
	if err != nil {
		return false, err
	}
	defer source.Close()
	length := opened.Descriptor().ByteSize
	var reader io.Reader = source
	if a.docxPlugin {
		text, err := t.runs.docxText(ctx, a, c.Ref, source)
		if errors.Is(err, docx.ErrInvalid) || errors.Is(err, docx.ErrLimit) || errors.Is(err, evidence.ErrInvalid) || errors.Is(err, artifact.ErrLimit) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		reader = strings.NewReader(text)
		length = int64(len(text))
	}
	input, _ := json.Marshal(contract.BodyRequest{Query: query})
	if length < int64(len(query)) {
		return false, nil
	}
	windowSize := int(min(int64(artifactSearchWindow), length))
	overlap := len(query) - 1
	buf := make([]byte, windowSize+overlap)
	carry := 0
	for {
		n, readErr := io.ReadFull(reader, buf[carry:carry+windowSize])
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return false, readErr
		}
		if n == 0 {
			return false, nil
		}
		window := buf[:carry+n]
		output, err := host.ReadService(ctx, a.pluginGraph, contract.BodyMatch, input, []rsi.InputData{{ID: contract.BodyInput, Data: window}})
		if err != nil {
			return false, err
		}
		var result contract.BodyResult
		if err := rsiplugin.DecodeResult(output, &result); err != nil {
			return false, err
		}
		if result.Match || readErr != nil {
			return result.Match, nil
		}
		carry = copy(buf, window[len(window)-min(overlap, len(window)):])
	}
}
