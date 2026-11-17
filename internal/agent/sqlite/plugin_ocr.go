package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/media"
	"github.com/z-chenhao/assistant/internal/media/ocr"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/contract"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/driver"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (t *tools) pluginOCR(ctx context.Context, inv tool.Invocation, ref, kind string, data []byte) (ocr.Document, error) {
	if t.runs.plugins == nil {
		return ocr.Document{}, ocr.ErrUnavailable
	}
	host := t.runs.pluginHost(t.claim)
	store := t.runs.host.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error {
		_, name, _, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if name != ocr.Name {
			return tool.ErrApprovalDenied
		}
		scope, err := runScope(ctx, tx, t.claim.RunID())
		if err != nil {
			return err
		}
		_, err = t.runs.host.Artifacts().DescribeTx(ctx, tx, scope, ref, true)
		return err
	}
	host.Store = store
	engine, _ := t.runs.imageOCR.(driver.Engine)
	host.Compute = driver.New(engine)
	revision, err := host.Store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return ocr.Document{}, err
	}
	if revision == "" {
		return ocr.Document{}, ocr.ErrUnavailable
	}
	binding, err := host.ServiceInstance(ctx, revision, contract.Contract)
	if errors.Is(err, rsi.ErrNotFound) {
		return ocr.Document{}, ocr.ErrUnavailable
	}
	if err != nil {
		return ocr.Document{}, err
	}
	languages := []string{}
	if e, ok := t.runs.imageOCR.(interface{ Languages() []string }); ok {
		languages = e.Languages()
	}
	input, _ := json.Marshal(contract.Request{InputID: "image", MediaType: kind, Languages: languages})
	raw, err := host.CallWithInput(ctx, rsiplugin.Execution{RunID: t.claim.RunID()}, binding.InstanceID, contract.Contract, "ocr:"+inv.ID, input, []rsi.InputData{{ID: "image", Data: data}})
	if err != nil {
		return ocr.Document{}, err
	}
	var response contract.Response
	if err = rsiplugin.DecodeResult(raw, &response); err != nil {
		return ocr.Document{}, err
	}
	switch response.ErrorCode {
	case "INVALID_ARGUMENT":
		return ocr.Document{}, media.ErrInvalid
	case "RESOURCE_LIMIT":
		return ocr.Document{}, media.ErrLimit
	case "PERMISSION_DENIED":
		return ocr.Document{}, ocr.ErrChanged
	case "PROVIDER_UNAVAILABLE":
		return ocr.Document{}, ocr.ErrUnavailable
	case "":
	default:
		return ocr.Document{}, rsi.ErrInvalid
	}
	if response.Document == nil {
		return ocr.Document{}, rsi.ErrInvalid
	}
	// Package quality is evolvable; source identity, byte/pixel budgets and box
	// containment remain Host invariants, independently of the package schema.
	img, err := media.DecodeImage(kind, data)
	if err != nil {
		return ocr.Document{}, err
	}
	doc := response.Document
	if doc.Width != img.Bounds().Dx() || doc.Height != img.Bounds().Dy() || len(doc.Text) > 128<<10 || len(doc.Words) > 4096 {
		return ocr.Document{}, rsi.ErrInvalid
	}
	for _, w := range doc.Words {
		if w.X < 0 || w.Y < 0 || w.Width < 1 || w.Height < 1 || w.X > doc.Width-w.Width || w.Y > doc.Height-w.Height {
			return ocr.Document{}, rsi.ErrInvalid
		}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return ocr.Document{}, agent.ErrStorage
	}
	var out ocr.Document
	if json.Unmarshal(encoded, &out) != nil {
		return out, rsi.ErrInvalid
	}
	return out, nil
}
