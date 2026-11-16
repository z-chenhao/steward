package rsi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"unicode/utf8"

	webextract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent/parserresource"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (h Host) parserProcess() parserresource.Process {
	if h.Parser != nil {
		return *h.Parser
	}
	return parserresource.Process{Vault: parserresource.Vault{Root: filepath.Join(filepath.Dir(h.Catalog.Root), "web-parser-resources")}, Launcher: h.Catalog.Launcher}
}

func (h Host) computeWebExtract(ctx context.Context, b rsiplugin.Binding, request webextract.Request, source InputData) (Computation, error) {
	var config webextract.Config
	if rsiplugin.Decode(b.Config, &config) != nil || config.RuntimeRevision == "" || config.SourceIdentity == "" {
		return Computation{}, ErrRevoked
	}
	pkg, err := h.Catalog.Load(b.PackageDigest)
	if err != nil {
		return Computation{}, err
	}
	declared := false
	for _, file := range pkg.Manifest.Files {
		if file.Path == webextract.ModuleFile {
			declared = true
		}
	}
	if !declared {
		return Computation{}, ErrInvalid
	}
	module, err := os.ReadFile(filepath.Join(h.Catalog.Path(b.PackageDigest), webextract.ModuleFile))
	if err != nil {
		return Computation{}, err
	}
	if config.RuntimeRevision == webextract.UnavailableRuntime {
		code := config.UnavailableError
		if code == "" {
			code = "WEB_EXTRACTOR_UNAVAILABLE"
		}
		if code != "WEB_EXTRACTOR_UNAVAILABLE" && code != "WEB_EXTRACTION_FAILED" {
			return Computation{}, ErrInvalid
		}
		raw, _ := json.Marshal(webextract.Metadata{ErrorCode: code})
		return Computation{Output: raw}, nil
	}
	process := h.parserProcess()
	revision, err := process.Vault.Derive(ctx, config.RuntimeRevision, module)
	if err != nil {
		return Computation{}, err
	}
	output, err := process.Parse(ctx, revision.Digest(), parserresource.Request{URL: request.URL, MediaType: request.MediaType, Data: source.Data})
	metadata := webextract.Metadata{}
	if err != nil {
		if ctx.Err() != nil {
			return Computation{}, ctx.Err()
		}
		if errors.Is(err, parserresource.ErrChanged) {
			return Computation{}, err
		}
		metadata.ErrorCode = "WEB_EXTRACTION_FAILED"
		raw, _ := json.Marshal(metadata)
		return Computation{Output: raw}, nil
	}
	data := append([]byte(output.Title), []byte(output.Text)...)
	if len(data) > 8<<20 || !utf8.Valid(data) {
		return Computation{}, ErrInvalid
	}
	metadata.TitleBytes, metadata.TextBytes = len(output.Title), len(output.Text)
	raw, _ := json.Marshal(metadata)
	return Computation{Output: raw, Data: data}, nil
}

// ExtractService is the original page owner's pure decoder. Candidate code
// receives no Run/Tool identity or external operation; the owning Tool keeps
// authorization, source facts, publication and recovery. Verification uses the
// same real candidate-module computation instead of synthetic engine replies.
func (h Host) ExtractService(ctx context.Context, revision string, request webextract.Request, data []byte, check func(context.Context) error) (parserresource.Response, error) {
	var empty parserresource.Response
	fence := func(c context.Context) error {
		if check == nil {
			return ErrRevoked
		}
		if err := check(c); err != nil {
			return err
		}
		return h.Store.CheckGraph(c, revision)
	}
	if err := fence(ctx); err != nil {
		return empty, err
	}
	b, err := h.ServiceInstance(ctx, revision, webextract.Contract)
	if err != nil {
		return empty, err
	}
	pkg, err := h.Catalog.Load(b.PackageDigest)
	if err != nil {
		return empty, err
	}
	service, ok := findService(pkg.Manifest, webextract.Contract)
	input, _ := json.Marshal(request)
	if !ok || !hasRequest(pkg.Manifest, "compute.web-extract") || !hasRequest(pkg.Manifest, "input.read") || hasRequest(pkg.Manifest, "state.run.write") || hasRequest(pkg.Manifest, "evolution.propose") || !tool.Validate(string(service.InputSchema), string(input)) {
		return empty, ErrRevoked
	}
	scope, err := newInputScope([]InputData{{ID: request.InputID, Data: data}})
	if err != nil {
		return empty, err
	}
	runner := h.withCompute(Runner{Catalog: h.Catalog}, &scope, fence, b, webextract.Contract, input)
	runner.ReadInput = func(c context.Context, in rsiplugin.InputRead) (rsiplugin.InputChunk, error) {
		if err := fence(c); err != nil {
			return rsiplugin.InputChunk{}, err
		}
		return scope.read(c, in)
	}
	var output rsiplugin.Result
	err = runner.Invoke(ctx, b, "call", rsiplugin.Call{OperationID: "owner-extract", Execution: rsiplugin.Execution{GraphRevision: revision}, Contract: webextract.Contract, Input: input, Inputs: scope.descriptor, State: rsiplugin.State{Value: pkg.Manifest.State.Initial}}, &output)
	if authority := fence(ctx); authority != nil {
		return empty, authority
	}
	if err != nil {
		return empty, err
	}
	plan, err := validateExtractComputation(&scope, output.Output)
	if output.State != nil || !tool.Validate(string(service.OutputSchema), string(output.Output)) {
		return empty, ErrInvalid
	}
	if err != nil {
		return empty, err
	}
	if plan.ErrorCode != "" {
		return empty, errors.New(plan.ErrorCode)
	}
	return parserresource.Response{Title: string(scope.computed[:plan.TitleBytes]), Text: string(scope.computed[plan.TitleBytes:])}, nil
}

func validateExtractComputation(scope *inputScope, raw json.RawMessage) (webextract.Plan, error) {
	var plan webextract.Plan
	var metadata webextract.Metadata
	if !scope.computedAttempted || rsiplugin.DecodeResult(raw, &plan) != nil || rsiplugin.Decode(scope.computedOutput, &metadata) != nil || plan.Metadata != metadata {
		return plan, ErrInvalid
	}
	if metadata.ErrorCode != "" {
		if plan.Digest != "" || len(scope.computed) != 0 {
			return plan, ErrInvalid
		}
		return plan, nil
	}
	if plan.Digest != rsiplugin.FileDigest(scope.computed) || metadata.TitleBytes < 0 || metadata.TextBytes < 1 || metadata.TitleBytes+metadata.TextBytes != len(scope.computed) || !utf8.Valid(scope.computed[:metadata.TitleBytes]) || !utf8.Valid(scope.computed[metadata.TitleBytes:]) {
		return plan, ErrInvalid
	}
	return plan, nil
}

// CaptureWebParser is an authenticated owner-composition operation. It is not
// an SDK callback and cannot be selected by candidate code.
func (h Host) CaptureWebParser(ctx context.Context, command string, module []byte) (parserresource.Revision, error) {
	return h.parserProcess().Vault.Capture(ctx, command, module)
}

// WebParserReference executes a separately captured original owner method for
// migration acceptance. It is not registered with candidate or model callers.
func (h Host) WebParserReference(ctx context.Context, revision string, input parserresource.Request) (parserresource.Response, error) {
	return h.parserProcess().Parse(ctx, revision, input)
}
