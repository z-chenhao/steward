package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/media/docx"
	docxreader "github.com/z-chenhao/assistant/internal/plugin/docxreader/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (r *Runs) docxText(ctx context.Context, access evidenceAccess, ref string, reader io.Reader) (string, error) {
	// Historical Runs retain their original native interpreter.
	if !access.docxPlugin {
		return docx.Text(ctx, reader)
	}
	if r.plugins == nil || access.pluginGraph == "" {
		return "", evidence.ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(reader, docx.MaxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > docx.MaxArchiveBytes {
		return "", docx.ErrLimit
	}
	host := *r.plugins
	store := r.host.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error {
		if err := r.evidenceFence(ctx, tx, access); err != nil {
			return err
		}
		_, err := r.host.Artifacts().DescribeTx(ctx, tx, access.scope, ref, true)
		return err
	}
	host.Store = store
	input, _ := json.Marshal(docxreader.Request{InputID: "document"})
	files := []rsi.InputData{{ID: "document", Data: data}}
	var raw json.RawMessage
	if access.claim == nil {
		raw, err = host.ReadService(ctx, access.pluginGraph, docxreader.Contract, input, files)
	} else {
		binding, e := host.ServiceInstance(ctx, access.pluginGraph, docxreader.Contract)
		if e != nil {
			if errors.Is(e, rsi.ErrNotFound) {
				return "", evidence.ErrUnavailable
			}
			return "", e
		}
		// Pure extraction is keyed by the exact source bytes. Pagination reuses
		// the same committed extraction; it cannot silently select a new parser.
		key := "docx:" + rsiplugin.FileDigest(data)
		raw, err = host.CallWithInput(ctx, rsiplugin.Execution{RunID: access.claim.RunID()}, binding.InstanceID, docxreader.Contract, key, input, files)
	}
	if err != nil {
		if errors.Is(err, rsi.ErrNotFound) {
			return "", evidence.ErrUnavailable
		}
		return "", err
	}
	var response docxreader.Response
	if err = rsiplugin.Decode(raw, &response); err != nil {
		return "", err
	}
	switch response.ErrorCode {
	case "":
		return response.Text, nil
	case "INVALID_ARGUMENT":
		return "", fmt.Errorf("%w: %s", evidence.ErrInvalid, docx.ErrInvalid)
	case "RESOURCE_LIMIT":
		return "", fmt.Errorf("%w: %s", artifact.ErrLimit, docx.ErrLimit)
	default:
		return "", rsi.ErrInvalid
	}
}

// A successful activation is the migration boundary. Once chosen, disabling
// the feature must not resurrect its old native implementation for new work.
// Existing Runs keep their separately frozen interpreter policy.
func managedDocx(ctx context.Context, tx *sql.Tx) (bool, error) {
	return managedService(ctx, tx, docxreader.Contract)
}

func managedService(ctx context.Context, tx *sql.Tx, contract string) (bool, error) {
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation a JOIN rsi_graph g ON g.revision=json_extract(a.receipt_json,'$.graph_revision'),json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') s WHERE json_extract(s.value,'$.contract')=?)`, contract).Scan(&enabled)
	return enabled, err
}
