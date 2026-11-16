package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/media"
	"github.com/z-chenhao/assistant/internal/media/ocr"
	"github.com/z-chenhao/assistant/internal/tool"
)

// ImageOCR is the local content processor used by the Host. It receives only
// authorized bytes, never database access, credentials or a model-chosen path.
type ImageOCR interface {
	ResourceHash() string
	Extract(context.Context, string, []byte) (ocr.Document, error)
}

func (r *Runs) ConfigureImageOCR(engine ImageOCR) { r.imageOCR = engine }

func (t *tools) executeImageOCR(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	var args, identity string
	var managed bool
	err := t.runs.write(ctx, func(tx *sql.Tx) error {
		_, name, accepted, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if name != ocr.Name {
			return tool.ErrApprovalDenied
		}
		args = accepted
		if err = tx.QueryRowContext(ctx, `SELECT coalesce(json_extract(policy_json,'$.image_ocr_plugin'),0) FROM run_context_policy WHERE run_id=?`, t.claim.RunID()).Scan(&managed); err != nil && err != sql.ErrNoRows {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT json_extract(j.value,'$.resource_hash') FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) j WHERE r.run_id=? AND json_extract(j.value,'$.name')=?`, t.claim.RunID(), name).Scan(&identity)
	})
	if err != nil {
		if ctx.Err() != nil {
			return imageOCRContextFailure(ctx.Err()), nil
		}
		return tool.Result{}, err
	}
	var request struct {
		Ref string `json:"artifactRef"`
	}
	if json.Unmarshal([]byte(args), &request) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	request.Ref = tool.ObjectRef("artifact", request.Ref)
	if t.runs.imageOCR == nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	if t.runs.imageOCR.ResourceHash() != identity {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	kind, data, err := t.runs.readImage(ctx, request.Ref, func(tx *sql.Tx) (artifact.Scope, error) {
		if err := t.fence(ctx, tx); err != nil {
			return artifact.Scope{}, err
		}
		return runScope(ctx, tx, t.claim.RunID())
	})
	if err != nil {
		if ctx.Err() != nil {
			return imageOCRContextFailure(ctx.Err()), nil
		}
		if errors.Is(err, agent.ErrClaimLost) || errors.Is(err, agent.ErrStorage) {
			return tool.Result{}, err
		}
		return artifactFailure(err), nil
	}
	var document ocr.Document
	if managed {
		document, err = t.pluginOCR(ctx, inv, request.Ref, kind, data)
	} else {
		document, err = t.runs.imageOCR.Extract(ctx, kind, data)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		if errors.Is(err, agent.ErrClaimLost) || errors.Is(err, agent.ErrStorage) {
			return tool.Result{}, err
		}
		code := "PROVIDER_UNAVAILABLE"
		switch {
		case errors.Is(err, context.Canceled):
			code = "CANCELLED"
		case errors.Is(err, context.DeadlineExceeded):
			code = "TIMEOUT"
		case errors.Is(err, media.ErrInvalid):
			code = "INVALID_ARGUMENT"
		case errors.Is(err, media.ErrLimit):
			code = "RESOURCE_LIMIT"
		case errors.Is(err, ocr.ErrChanged):
			code = "PERMISSION_DENIED"
		}
		return tool.Failure(code), nil
	}
	document.SourceRef = request.Ref
	raw, err := json.Marshal(document)
	if err != nil {
		return tool.Result{}, agent.ErrStorage
	}
	return tool.Result{ArtifactRefs: []string{request.Ref}, Output: &tool.BinaryOutput{
		Name: "image-ocr.json", MediaType: "application/json", Reader: bytes.NewReader(raw), ResultJSON: document.Preview,
	}}, nil
}

func imageOCRContextFailure(err error) tool.Result {
	if errors.Is(err, context.DeadlineExceeded) {
		return tool.Failure("TIMEOUT")
	}
	return tool.Failure("CANCELLED")
}
