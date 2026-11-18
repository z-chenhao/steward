package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/plugin/filetools/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type fileSession interface {
	ResourceHash() string
	SnapshotFile(context.Context, string) (environment.FileSnapshot, *tool.Error)
	ApplyFile(context.Context, environment.FileChange) tool.Result
}

type localEnvironmentSession struct {
	environment.Local
	scope string
}

func (s localEnvironmentSession) ResourceHash() string { return s.scope }

type guestFileDriver interface {
	EnvironmentResourceHash() string
	SnapshotEnvironmentFile(context.Context, computer.Instance, string) (environment.FileSnapshot, *tool.Error)
	ApplyEnvironmentFile(context.Context, computer.Instance, environment.FileChange) tool.Result
}

type guestFileSession struct {
	driver   guestFileDriver
	instance computer.Instance
}

func (s guestFileSession) ResourceHash() string { return s.driver.EnvironmentResourceHash() }

func (s guestFileSession) SnapshotFile(ctx context.Context, path string) (environment.FileSnapshot, *tool.Error) {
	return s.driver.SnapshotEnvironmentFile(ctx, s.instance, path)
}
func (s guestFileSession) ApplyFile(ctx context.Context, c environment.FileChange) tool.Result {
	return s.driver.ApplyEnvironmentFile(ctx, s.instance, c)
}

type fileArguments struct {
	Path       string      `json:"path"`
	AsArtifact bool        `json:"as_artifact"`
	Offset     json.Number `json:"offset"`
	Limit      json.Number `json:"limit"`
	BeforeHash *string     `json:"before_hash"`
	OldText    string      `json:"old_text"`
	NewText    string      `json:"new_text"`
	Content    string      `json:"content"`
}

func environmentMethodFailure(err error) (tool.Result, error) {
	if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) || errors.Is(err, rsi.ErrInvalid) || errors.Is(err, rsi.ErrUnknown) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	if errors.Is(err, tool.ErrReplay) {
		return tool.Failure("NON_DETERMINISTIC_REPLAY"), nil
	}
	if errors.Is(err, tool.ErrApprovalDenied) {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	return tool.Result{}, err
}

// The original caller owns workspace selection, lease cancellation, concrete
// approval, never-replay and Computer lifecycle. This method owns only pure
// policy selection and the acceptance of that choice before the original IO.
func (t *tools) executeManagedFile(ctx context.Context, inv tool.Invocation, driver fileSession) (tool.Result, error) {
	var args fileArguments
	var requestHash, resource string
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		role, name, raw, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if role != "worker" || !environment.IsFileTool(name) {
			return tool.ErrApprovalDenied
		}
		if err = json.Unmarshal([]byte(raw), &args); err != nil {
			return err
		}
		inv.Call.Name, inv.Call.Arguments = name, raw
		requestHash = digest(name + ":" + raw)
		return tx.QueryRowContext(ctx, `SELECT json_extract(j.value,'$.resource_hash') FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) j WHERE r.run_id=? AND json_extract(j.value,'$.name')=?`, t.claim.RunID(), name).Scan(&resource)
	})
	if err != nil {
		return environmentMethodFailure(err)
	}
	if resource == "" || driver.ResourceHash() != resource {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	if t.runs.plugins == nil {
		return environmentMethodFailure(rsi.ErrNotFound)
	}
	h := t.runs.pluginHostWithFence(t.fence)
	revision, err := h.Store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return environmentMethodFailure(err)
	}
	if revision == "" {
		return environmentMethodFailure(rsi.ErrNotFound)
	}
	window, err := h.ServiceInstance(ctx, revision, contract.WindowContract)
	if err != nil {
		return environmentMethodFailure(err)
	}
	edit, err := h.ServiceInstance(ctx, revision, contract.EditContract)
	if err != nil {
		return environmentMethodFailure(err)
	}
	if window.InstanceID != edit.InstanceID || window.PackageDigest != edit.PackageDigest {
		return environmentMethodFailure(rsi.ErrInvalid)
	}
	if err = h.Store.CheckGraph(ctx, revision); err != nil {
		return environmentMethodFailure(err)
	}
	snapshot, failure := driver.SnapshotFile(ctx, args.Path)
	if failure != nil {
		return tool.Result{Error: failure}, nil
	}
	if inv.Call.Name == "builtin__read" {
		if !snapshot.Exists {
			return tool.Failure("NOT_FOUND"), nil
		}
		return t.readFileMethod(ctx, h, revision, args, snapshot)
	}
	if !snapshot.Exists {
		if inv.Call.Name == "builtin__edit" || args.BeforeHash != nil {
			return tool.Failure("NOT_FOUND"), nil
		}
	} else if args.BeforeHash == nil || *args.BeforeHash != snapshot.Hash {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	change := environment.FileChange{Operation: strings.TrimPrefix(inv.Call.Name, "builtin__"), Path: args.Path, BeforeHash: args.BeforeHash, OldText: args.OldText, NewText: args.NewText, Content: args.Content}
	identity, err := environment.FileDriverFingerprint()
	if err != nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	identity = digest(resource + ":" + identity)
	var savedGraph, savedDriver, savedRequest string
	var savedSource sql.NullString
	var savedSpan sql.NullInt64
	cached := false
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT graph_revision,driver_hash,request_hash,source_hash,span_start FROM file_method_plan WHERE invocation_id=?`, inv.ID).Scan(&savedGraph, &savedDriver, &savedRequest, &savedSource, &savedSpan)
		if err == sql.ErrNoRows {
			return nil
		}
		cached = err == nil
		return err
	})
	if err != nil {
		return tool.Result{}, err
	}
	if cached {
		if savedGraph != revision || savedDriver != identity || savedRequest != requestHash {
			return environmentMethodFailure(tool.ErrReplay)
		}
		if savedSource.Valid != snapshot.Exists || (snapshot.Exists && savedSource.String != snapshot.Hash) {
			return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
		}
		if savedSpan.Valid {
			change.Start = int(savedSpan.Int64)
		}
	} else if change.Operation == "edit" {
		if args.OldText == "" || !utf8.Valid(snapshot.Data) || bytes.Count(snapshot.Data, []byte(args.OldText)) != 1 {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		input, _ := json.Marshal(contract.EditFacts{InputID: "file", OldText: args.OldText})
		raw, err := h.ReadService(ctx, revision, contract.EditContract, input, []rsi.InputData{{ID: "file", Data: snapshot.Data}})
		if err != nil {
			return environmentMethodFailure(err)
		}
		var span contract.EditSpan
		if rsiplugin.DecodeResult(raw, &span) != nil {
			return environmentMethodFailure(rsi.ErrInvalid)
		}
		if span.ErrorCode == "INVALID_ARGUMENT" && span.Start == nil {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		if span.ErrorCode != "" || span.Start == nil {
			return environmentMethodFailure(rsi.ErrInvalid)
		}
		change.Start = *span.Start
	}
	var replacement []byte
	if change.Operation == "edit" {
		start, end := change.Start, change.Start+len([]byte(args.OldText))
		if start < 0 || end < start || end > len(snapshot.Data) || !bytes.Equal(snapshot.Data[start:end], []byte(args.OldText)) || bytes.Count(snapshot.Data, []byte(args.OldText)) != 1 {
			return environmentMethodFailure(rsi.ErrInvalid)
		}
		if len(snapshot.Data)-len(args.OldText)+len(args.NewText) > 8<<20 {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		replacement = append(append(append([]byte{}, snapshot.Data[:start]...), []byte(args.NewText)...), snapshot.Data[end:]...)
	} else {
		replacement = []byte(args.Content)
	}
	if len(replacement) > 8<<20 {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	err = t.runs.write(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		if err := t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID()); err != nil {
			return err
		}
		if cached {
			return nil
		}
		var source any
		var span any
		if snapshot.Exists {
			source = snapshot.Hash
		}
		if change.Operation == "edit" {
			span = change.Start
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO file_method_plan VALUES(?,?,?,?,?,?)`, inv.ID, revision, identity, requestHash, source, span)
		return err
	})
	if err != nil {
		return environmentMethodFailure(err)
	}
	result := driver.ApplyFile(ctx, change)
	if result.Error != nil {
		return result, nil
	}
	var value struct {
		Path  string `json:"path"`
		Hash  string `json:"hash"`
		Bytes int    `json:"bytes"`
	}
	if json.Unmarshal([]byte(result.Text), &value) != nil || value.Path != args.Path || value.Hash != fmt.Sprintf("%x", sha256.Sum256(replacement)) || value.Bytes != len(replacement) {
		return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
	}
	return result, nil
}

func (t *tools) readFileMethod(ctx context.Context, h rsi.Host, revision string, args fileArguments, snapshot environment.FileSnapshot) (tool.Result, error) {
	offset := big.NewInt(1)
	if args.Offset != "" {
		var ok bool
		offset, ok = new(big.Int).SetString(string(args.Offset), 10)
		if !ok {
			return tool.Failure("PERMISSION_DENIED"), nil
		}
		if offset.Sign() < 1 {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
	}
	limit := 200
	if args.Limit != "" {
		value, ok := new(big.Int).SetString(string(args.Limit), 10)
		if !ok {
			return tool.Failure("PERMISSION_DENIED"), nil
		}
		if !value.IsInt64() || value.Int64() < 1 || value.Int64() > 2000 {
			return tool.Failure("INVALID_ARGUMENT"), nil
		}
		limit = int(value.Int64())
	}
	if limit < 1 || limit > 2000 {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	effective := len(snapshot.Data) + 1
	if offset.IsInt64() && offset.Int64() < int64(effective) {
		effective = int(offset.Int64())
	}
	facts := contract.WindowFacts{InputID: "file", Offset: effective, Limit: limit, OutputBytes: 48000, AsArtifact: args.AsArtifact}
	input, _ := json.Marshal(facts)
	raw, err := h.ReadService(ctx, revision, contract.WindowContract, input, []rsi.InputData{{ID: "file", Data: snapshot.Data}})
	if err != nil {
		return environmentMethodFailure(err)
	}
	var plan contract.Window
	if rsiplugin.DecodeResult(raw, &plan) != nil {
		return environmentMethodFailure(rsi.ErrInvalid)
	}
	if plan.Artifact {
		if (!args.AsArtifact && utf8.Valid(snapshot.Data)) || plan.Start != 0 || plan.End != 0 || plan.NextOffset != 0 {
			return environmentMethodFailure(rsi.ErrInvalid)
		}
		return tool.Result{Output: &tool.BinaryOutput{Name: filepath.Base(args.Path), MediaType: "application/octet-stream", Reader: bytes.NewReader(snapshot.Data), ResultJSON: func(ref string) string {
			raw, _ := json.Marshal(map[string]string{"path": args.Path, "artifactRef": ref})
			return string(raw)
		}}}, nil
	}
	if args.AsArtifact || !utf8.Valid(snapshot.Data) || !validFileWindow(snapshot.Data, facts, plan) {
		return environmentMethodFailure(rsi.ErrInvalid)
	}
	next := new(big.Int).Add(offset, big.NewInt(int64(plan.NextOffset-effective)))
	value := struct {
		Path      string      `json:"path"`
		Text      string      `json:"text"`
		Hash      string      `json:"hash"`
		Offset    json.Number `json:"offset"`
		Next      json.Number `json:"next_offset"`
		Truncated bool        `json:"truncated"`
	}{args.Path, string(snapshot.Data[plan.Start:plan.End]), snapshot.Hash, json.Number(offset.String()), json.Number(next.String()), plan.End < len(snapshot.Data)}
	raw, err = json.Marshal(value)
	return tool.Result{Text: string(raw)}, err
}

// This independent Host check authorizes only a contiguous original-byte
// prefix within the requested lines. Candidate text or invented continuation
// coordinates cannot become file evidence.
func validFileWindow(data []byte, f contract.WindowFacts, p contract.Window) bool {
	start, line := 0, 1
	for line < f.Offset && start < len(data) {
		start = hostFileLineEnd(data, start)
		line++
	}
	if p.Start != start || p.End < start || p.End > len(data) || p.End-start > f.OutputBytes || !utf8.Valid(data[start:p.End]) {
		return false
	}
	if start == len(data) {
		return p.End == start && p.NextOffset == f.Offset
	}
	if p.End == start {
		return false
	}
	position, count := start, 0
	for position < p.End {
		end := hostFileLineEnd(data, position)
		count++
		if end > p.End {
			if count != 1 || end-start <= f.OutputBytes {
				return false
			}
			clipped := start + f.OutputBytes
			for clipped > start && !utf8.Valid(data[start:clipped]) {
				clipped--
			}
			if p.End != clipped {
				return false
			}
			position = p.End
		} else {
			position = end
		}
	}
	return count <= f.Limit && p.NextOffset == f.Offset+count
}

func hostFileLineEnd(data []byte, start int) int {
	for p := start; p < len(data); {
		r, size := utf8.DecodeRune(data[p:])
		p += size
		switch r {
		case '\r':
			if p < len(data) && data[p] == '\n' {
				p++
			}
			return p
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', '\u2028', '\u2029':
			return p
		}
	}
	return len(data)
}
