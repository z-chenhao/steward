package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	artifactsqlite "github.com/z-chenhao/assistant/internal/artifact/sqlite"
	artifacttools "github.com/z-chenhao/assistant/internal/artifact/tools"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/tool"
)

func runScope(ctx context.Context, tx *sql.Tx, id string) (artifact.Scope, error) {
	a := artifact.Scope{RunID: id}
	err := tx.QueryRowContext(ctx, `SELECT agent_id,coalesce(task_id,''),coalesce((SELECT user_id FROM runtime_owner WHERE singleton=1),'local') FROM run WHERE run_id=?`, id).Scan(&a.AgentID, &a.TaskID, &a.UserID)
	return a, err
}
func artifactFailure(err error) tool.Result {
	switch {
	case errors.Is(err, artifact.ErrInvalid):
		return tool.Failure("INVALID_ARGUMENT")
	case errors.Is(err, artifact.ErrNotFound), errors.Is(err, artifact.ErrIntegrity):
		return tool.Failure("NOT_FOUND")
	case errors.Is(err, artifact.ErrLimit):
		return tool.Failure("RESOURCE_LIMIT")
	default:
		return tool.Failure("PROVIDER_UNAVAILABLE")
	}
}

// executeArtifact validates the accepted Host call; writes are deferred to
// storeOutput so metadata/publication and the Tool result commit atomically.
func (t *tools) executeArtifact(ctx context.Context, tx *sql.Tx, name, args string) (tool.Result, error) {
	a, err := runScope(ctx, tx, t.claim.RunID())
	if err != nil {
		return tool.Result{}, err
	}
	var v struct {
		Operation string   `json:"operation"`
		Query     string   `json:"query"`
		Name      string   `json:"name"`
		MediaType string   `json:"mediaType"`
		Text      string   `json:"text"`
		Ref       string   `json:"artifactRef"`
		Refs      []string `json:"artifactRefs"`
		Offset    int64    `json:"offset"`
		Limit     int      `json:"limit"`
	}
	if json.Unmarshal([]byte(args), &v) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	v.Ref = tool.ObjectRef("artifact", v.Ref)
	for i, ref := range v.Refs {
		v.Refs[i] = tool.ObjectRef("artifact", ref)
	}
	store := t.runs.host.Artifacts()
	operation := v.Operation
	if name == artifacttools.Search {
		operation = "search"
	}
	switch operation {
	case "save":
		return tool.Result{Output: &tool.BinaryOutput{Name: v.Name, MediaType: v.MediaType, Reader: strings.NewReader(v.Text)}}, nil
	case "publish":
		if v.Refs == nil {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		return tool.Result{Text: "Final artifacts selected.", PublishRefs: v.Refs}, nil
	case "describe":
		d, e := store.DescribeTx(ctx, tx, a, v.Ref, true)
		if e != nil {
			return artifactFailure(e), nil
		}
		raw, e := encoded(d)
		return tool.Result{Text: raw}, e
	case "search":
		list, e := store.SearchTx(ctx, tx, a, v.Query, v.Limit)
		if e != nil {
			return artifactFailure(e), nil
		}
		raw, e := encoded(list)
		return tool.Result{Text: raw}, e

	}
	return tool.Failure("NOT_FOUND"), nil
}

// artifactChange rolls back normalization before recording an expected Tool
// failure. The terminal result uses a live write context even when its input
// stream/deadline has ended; no partial metadata, pins or publications survive.
func artifactChange(ctx, outputCtx context.Context, tx *sql.Tx, change func() error) error {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT artifact_output`); err != nil {
		return err
	}
	err := change()
	if outputCtx.Err() != nil {
		err = outputCtx.Err()
	}
	if err != nil {
		if _, e := tx.ExecContext(ctx, `ROLLBACK TO artifact_output`); e != nil {
			return artifact.ErrStorage
		}
	}
	if _, e := tx.ExecContext(ctx, `RELEASE artifact_output`); e != nil {
		return artifact.ErrStorage
	}
	return err
}

func (r *Runs) storeOutput(ctx, outputCtx context.Context, tx *sql.Tx, runID, id string, result tool.Result, prepared preparedOutput) (tool.Result, error) {
	a, err := runScope(ctx, tx, runID)
	if err != nil {
		return tool.Result{}, err
	}
	store := r.host.Artifacts()
	if len(result.ImageRefs) > 0 {
		if len(result.ImageRefs) > 8 || !imageRefsIncluded(result.ImageRefs, result.ArtifactRefs) {
			return tool.Failure("PERMISSION_DENIED"), nil
		}
		for _, ref := range result.ImageRefs {
			d, err := store.DescribeTx(outputCtx, tx, a, ref, true)
			if err != nil || !strings.HasPrefix(d.MediaType, "image/") {
				return tool.Failure("PERMISSION_DENIED"), nil
			}
		}
	}
	var policy, name, args string
	if err = tx.QueryRowContext(ctx, `SELECT policy_json,tool_name,arguments_json FROM tool_invocation WHERE invocation_id=? AND run_id=?`, id, a.RunID).Scan(&policy, &name, &args); err != nil {
		if err == sql.ErrNoRows {
			return tool.Result{}, agent.ErrConflict
		}
		return tool.Result{}, err
	}
	var request struct {
		Operation string `json:"operation"`
	}
	_ = json.Unmarshal([]byte(args), &request)
	if result.PublishRefs != nil && (name != artifacttools.Manage || request.Operation != "publish") {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	var p tool.Policy
	if json.Unmarshal([]byte(policy), &p) != nil {
		return tool.Result{}, artifact.ErrStorage
	}
	if p.OutputBytes == 0 {
		p.OutputBytes = 16384
	}
	if result.Error != nil {
		result = tool.Normalize(result)
		if name == memory.ManageName && result.Error.Code == "INVALID_ARGUMENT" {
			// Restore only Host-generated field names after provider error
			// sanitization. Frozen older capabilities retain their old receipt.
			var allowed bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) s WHERE r.run_id=? AND json_extract(s.value,'$.name')=? AND json_extract(s.value,'$.version')>=6)`, runID, name).Scan(&allowed); err != nil {
				return tool.Result{}, err
			}
			if allowed {
				result.Error.Message = memory.MutationError(args)
			}
		}
		return result, nil
	}
	// Agent results keep their two-field business envelope while reusing the
	// ordinary output/Artifact transaction for large text and reference pins.
	isAgentResult := name == agent.RunToolName
	if isAgentResult {
		var value agent.AgentResult
		if json.Unmarshal([]byte(result.Text), &value) != nil {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
		result.Text, result.ArtifactRefs = value.Text, value.ArtifactRefs
		p.OutputBytes = 16384 // Bounded business preview inside the API envelope.
	}
	pack := func(value tool.Result) (tool.Result, error) {
		if !isAgentResult || value.Error != nil {
			return value, nil
		}
		refs := value.ArtifactRefs
		if refs == nil {
			refs = []string{}
		}
		raw, err := encoded(agent.AgentResult{Text: value.Text, ArtifactRefs: refs})
		return tool.Result{Text: raw}, err
	}
	if result.Output == nil && len(result.Text) <= p.OutputBytes && len(result.ArtifactRefs) == 0 && result.PublishRefs == nil && len(result.Attachments) == 0 {
		return pack(result)
	}
	err = artifactChange(ctx, outputCtx, tx, func() error {
		if prepared.err != nil {
			return prepared.err
		}
		images := len(result.ImageRefs)
		for _, output := range result.Attachments {
			if output.Image {
				images++
			}
		}
		if images > 8 {
			return artifact.ErrLimit
		}
		for i, staged := range prepared.attachments {
			ref, e := store.CommitTx(ctx, tx, a, staged, "tool:"+id+":attachment:"+strconv.Itoa(i))
			if e != nil {
				return e
			}
			result.ArtifactRefs = append(result.ArtifactRefs, ref)
			if result.Attachments[i].Image {
				result.ImageRefs = append(result.ImageRefs, ref)
			}
		}
		result.Attachments = nil
		for _, ref := range result.ArtifactRefs {
			if _, err := store.DescribeTx(outputCtx, tx, a, ref, true); err != nil {
				return err
			}
		}
		if result.Output != nil || len(result.Text) > p.OutputBytes {
			output := result.Output
			if output == nil {
				output = &tool.BinaryOutput{Name: "tool-output.txt", MediaType: "text/plain", Reader: strings.NewReader(result.Text)}
			}
			if prepared.err != nil {
				return prepared.err
			}
			if prepared.staged == nil {
				return artifact.ErrInvalid
			}
			ref, e := store.CommitTx(ctx, tx, a, prepared.staged, "tool:"+id)
			if e != nil {
				return e
			}
			result.Text = "Saved artifact."
			if output.ResultJSON != nil {
				result.Text = output.ResultJSON(ref)
				if len(result.Text) > p.OutputBytes || !json.Valid([]byte(result.Text)) {
					return artifact.ErrLimit
				}
			}
			result.ArtifactRefs = append(result.ArtifactRefs, ref)
			result.Output = nil
		}
		for _, ref := range result.ArtifactRefs {
			if err := store.PinTx(outputCtx, tx, a, ref, "tool", id); err != nil {
				return err
			}
		}
		if result.PublishRefs != nil {
			if prepared.err != nil {
				return prepared.err
			}
			if err := store.PublishTx(outputCtx, tx, a, prepared.publication); err != nil {
				return err
			}
			result.ArtifactRefs = append(result.ArtifactRefs, result.PublishRefs...)
			result.PublishRefs = nil
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			if d, ok := r.registry.Resolve(name); ok && d.Spec.Permission == "external_effect" {
				return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
			}
		}
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return tool.Failure("TIMEOUT"), nil
		case errors.Is(err, context.Canceled):
			return tool.Failure("CANCELLED"), nil
		case errors.Is(err, artifact.ErrInvalid), errors.Is(err, artifact.ErrNotFound), errors.Is(err, artifact.ErrIntegrity), errors.Is(err, artifact.ErrLimit):
			return artifactFailure(err), nil
		default:
			return tool.Result{}, err
		}
	}
	return pack(result)
}

// Receipt storage failures cannot establish that an external effect failed.
// storeOutput rolls back normalization before returning an expected failure.
func (r *Runs) storeRecoveryOutput(ctx, outputCtx context.Context, tx *sql.Tx, runID, id string, result tool.Result, prepared preparedOutput) (tool.Result, error) {
	if outputCtx.Err() != nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	out, err := r.storeOutput(ctx, outputCtx, tx, runID, id, result, prepared)
	if err == nil && result.Error == nil && out.Error != nil {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	return out, err
}
func published(ctx context.Context, tx *sql.Tx, runID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT artifact_ref FROM artifact_publication WHERE run_id=? ORDER BY artifact_ref`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []string{}
	for rows.Next() {
		var ref string
		if err = rows.Scan(&ref); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func imageRefsIncluded(images, refs []string) bool {
	for _, image := range images {
		found := false
		for _, ref := range refs {
			found = found || ref == image
		}
		if !found {
			return false
		}
	}
	return true
}

// stageOutput performs provider I/O outside state.db. The terminal transaction
// repeats policy, owner, result and reference validation before publication.
type preparedOutput struct {
	attachments []*artifactsqlite.Staged
	staged      *artifactsqlite.Staged
	publication *artifactsqlite.Publication
	err         error
}

func (p preparedOutput) Close() {
	for _, staged := range p.attachments {
		staged.Close()
	}
	if p.staged != nil {
		p.staged.Close()
	}
}
func (r *Runs) stageOutput(ctx context.Context, id string, result tool.Result) preparedOutput {
	var prepared preparedOutput
	if len(result.Attachments) > 8 {
		prepared.err = artifact.ErrLimit
		return prepared
	}
	for _, output := range result.Attachments {
		if output.Reader == nil || (output.Image && output.MediaType != "image/jpeg" && output.MediaType != "image/png") {
			prepared.err = artifact.ErrInvalid
			return prepared
		}
		staged, err := r.host.Artifacts().Stage(ctx, artifact.Input{Name: output.Name, MediaType: output.MediaType}, output.Reader)
		if err != nil {
			prepared.err = err
			return prepared
		}
		prepared.attachments = append(prepared.attachments, staged)
	}
	prepared.staged, prepared.err = r.stageBinaryOutput(ctx, id, result)
	if prepared.err != nil || result.PublishRefs == nil {
		return prepared
	}
	var scope artifact.Scope
	var runID string
	prepared.err = r.host.db.QueryRowContext(ctx, `SELECT run_id FROM tool_invocation WHERE invocation_id=?`, id).Scan(&runID)
	if prepared.err != nil {
		return prepared
	}
	prepared.err = r.write(ctx, func(tx *sql.Tx) error { var err error; scope, err = runScope(ctx, tx, runID); return err })
	if prepared.err == nil {
		prepared.publication, prepared.err = r.host.Artifacts().PreparePublication(ctx, scope, result.PublishRefs)
	}
	return prepared
}
func (r *Runs) stageBinaryOutput(ctx context.Context, id string, result tool.Result) (*artifactsqlite.Staged, error) {
	if result.Error != nil {
		return nil, nil
	}
	var raw, name string
	if err := r.host.db.QueryRowContext(ctx, `SELECT policy_json,tool_name FROM tool_invocation WHERE invocation_id=?`, id).Scan(&raw, &name); err != nil {
		return nil, err
	}
	var policy tool.Policy
	if json.Unmarshal([]byte(raw), &policy) != nil {
		return nil, artifact.ErrStorage
	}
	if policy.OutputBytes == 0 {
		policy.OutputBytes = 16384
	}
	text := result.Text
	if name == agent.RunToolName {
		var value agent.AgentResult
		if json.Unmarshal([]byte(text), &value) != nil {
			return nil, nil
		}
		text = value.Text
		policy.OutputBytes = 16384
	}
	output := result.Output
	if output == nil && len(text) > policy.OutputBytes {
		output = &tool.BinaryOutput{Name: "tool-output.txt", MediaType: "text/plain", Reader: strings.NewReader(text)}
	}
	if output == nil {
		return nil, nil
	}
	return r.host.Artifacts().Stage(ctx, artifact.Input{Name: output.Name, MediaType: output.MediaType}, output.Reader)
}

func (t *tools) executeArtifactCall(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	result := tool.Failure("PERMISSION_DENIED")
	var readArgs string
	err := t.runs.write(ctx, func(tx *sql.Tx) error {
		_, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if errors.Is(err, tool.ErrApprovalDenied) {
			return nil
		}
		if err != nil {
			return err
		}
		if !artifacttools.Known(name) {
			return nil
		}
		var request struct {
			Operation string `json:"operation"`
		}
		if name == artifacttools.Manage && json.Unmarshal([]byte(args), &request) == nil && request.Operation == "read" {
			readArgs = args
			return nil
		}
		result, err = t.executeArtifact(ctx, tx, name, args)
		return err
	})
	if err == nil && readArgs != "" {
		return t.readArtifact(ctx, readArgs)
	}
	return result, err
}

type artifactReadRequest struct {
	Ref    string `json:"artifactRef"`
	Offset int64  `json:"offset"`
	Limit  int    `json:"limit"`
}

func (t *tools) readArtifact(ctx context.Context, args string) (tool.Result, error) {
	var v artifactReadRequest
	if json.Unmarshal([]byte(args), &v) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	v.Ref = tool.ObjectRef("artifact", v.Ref)

	f, d, e := t.runs.openArtifact(ctx, v.Ref, func(tx *sql.Tx) (artifact.Scope, error) {
		if err := t.fence(ctx, tx); err != nil {
			return artifact.Scope{}, err
		}
		return runScope(ctx, tx, t.claim.RunID())
	})
	if e != nil {
		if errors.Is(e, agent.ErrClaimLost) || errors.Is(e, agent.ErrStorage) {
			return tool.Result{}, e
		}
		return artifactFailure(e), nil
	}
	defer f.Close()
	// Binary media are passed by ref; never base64-encode them into history.
	media, _, _ := mime.ParseMediaType(d.MediaType)
	if !strings.HasPrefix(media, "text/") && media != "application/json" && !strings.HasSuffix(media, "+json") {
		return tool.Result{Text: "Binary content; use the artifact reference.", ArtifactRefs: []string{v.Ref}}, nil
	}

	if v.Limit == 0 {
		v.Limit = 4096
	}
	if v.Limit < 1 || v.Limit > 4096 || v.Offset < 0 || v.Offset > d.ByteSize {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if _, e = io.CopyN(io.Discard, f, v.Offset); e != nil {
		return artifactFailure(e), nil
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(v.Limit)))
	if e != nil {
		return artifactFailure(e), nil
	}
	// Byte ranges may cut a Unicode character. Reject that range explicitly.
	if !utf8.Valid(b) {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	raw, e := encoded(struct {
		Text   string `json:"text"`
		Offset int64  `json:"offset"`
		Bytes  int    `json:"bytes"`
		Total  int64  `json:"total_bytes"`
	}{string(b), v.Offset, len(b), d.ByteSize})
	return tool.Result{Text: raw}, e
}

func (r *Runs) openArtifact(ctx context.Context, ref string, authorize func(*sql.Tx) (artifact.Scope, error)) (io.ReadCloser, artifact.Descriptor, error) {
	var opened *artifactsqlite.Opened
	err := r.write(ctx, func(tx *sql.Tx) error {
		scope, err := authorize(tx)
		if err != nil {
			return err
		}
		opened, err = r.host.Artifacts().OpenFileTx(ctx, tx, scope, ref)
		return err
	})
	if err != nil {
		if opened != nil {
			opened.Close()
		}
		return nil, artifact.Descriptor{}, err
	}
	reader, err := opened.Verify(ctx)
	if err != nil {
		return nil, artifact.Descriptor{}, err
	}
	// A privacy/stop epoch can change while hashing. Revalidate before any
	// bytes reach a runtime or provider; returning the same fd avoids TOCTOU.
	err = r.write(ctx, func(tx *sql.Tx) error {
		scope, err := authorize(tx)
		if err != nil {
			return err
		}
		_, err = r.host.Artifacts().DescribeTx(ctx, tx, scope, ref, true)
		return err
	})
	if err != nil {
		reader.Close()
		return nil, artifact.Descriptor{}, err
	}
	return reader, opened.Descriptor(), nil
}
