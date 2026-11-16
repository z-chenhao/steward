package sqlite

import (
	"context"
	"database/sql"
	"unicode/utf8"

	"encoding/json"
	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// TraceWorkbenchProgram resolves only an explicit, previously accepted graph.
// It does not query observation bodies or grant the resource any source access.
func (s Inspector) TraceWorkbenchProgram(ctx context.Context, reference string) (observability.TraceProgram, error) {
	if reference == "" || !observability.ValidReaderRevision(reference) {
		return observability.TraceProgram{}, observability.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return observability.TraceProgram{}, err
	}
	defer tx.Rollback()
	host, revision, managed, err := s.traceReadSelection(ctx, tx, reference)
	if err != nil {
		return observability.TraceProgram{}, err
	}
	if !managed || revision != reference {
		return observability.TraceProgram{}, rsi.ErrNotFound
	}
	return readTraceWorkbenchProgram(ctx, host, revision)
}

func readTraceWorkbenchProgram(ctx context.Context, host rsi.Host, reference string) (observability.TraceProgram, error) {
	raw, err := host.ReadService(ctx, reference, contract.Workbench, json.RawMessage(`{}`), nil)
	if err != nil {
		return observability.TraceProgram{}, err
	}
	var result contract.WorkbenchResult
	if err = rsiplugin.DecodeResult(raw, &result); err != nil {
		return observability.TraceProgram{}, rsi.ErrInvalid
	}
	binding, err := host.ServiceInstance(ctx, reference, contract.Workbench)
	if err != nil {
		return observability.TraceProgram{}, err
	}
	pkg, err := host.Catalog.Load(binding.PackageDigest)
	if err != nil {
		return observability.TraceProgram{}, err
	}
	digest, styleDigest, templateDigest, copyDigest := "", "", "", ""
	for _, resource := range pkg.Manifest.Resources {
		if resource.Name == contract.CopyResource && resource.Kind == "view" && resource.Path == contract.CopyFile {
			for _, file := range pkg.Manifest.Files {
				if file.Path == resource.Path {
					copyDigest = file.Digest
				}
			}
		}
	}
	for _, resource := range pkg.Manifest.Resources {
		if resource.Name == contract.ProgramResource && resource.Kind == "view" && resource.Path == contract.ProgramFile {
			for _, file := range pkg.Manifest.Files {
				if file.Path == resource.Path {
					digest = file.Digest
				}
			}
		}
	}
	for _, resource := range pkg.Manifest.Resources {
		if resource.Name == contract.StylesheetResource && resource.Kind == "view" && resource.Path == contract.StylesheetFile {
			for _, file := range pkg.Manifest.Files {
				if file.Path == resource.Path {
					styleDigest = file.Digest
				}
			}
		}
	}
	for _, resource := range pkg.Manifest.Resources {
		if resource.Name == contract.TemplateResource && resource.Kind == "view" && resource.Path == contract.TemplateFile {
			for _, file := range pkg.Manifest.Files {
				if file.Path == resource.Path {
					templateDigest = file.Digest
				}
			}
		}
	}
	if err = acceptTraceTemplate(result.Template, templateDigest); err != nil {
		return observability.TraceProgram{}, err
	}
	if err = acceptTraceStylesheet(result.Stylesheet, styleDigest); err != nil {
		return observability.TraceProgram{}, err
	}

	if err = acceptTraceProgram(result.Program, digest); err != nil {
		return observability.TraceProgram{}, err
	}
	translations, err := acceptTraceCopy(result.Copy, copyDigest)
	if err != nil {
		return observability.TraceProgram{}, err
	}

	return observability.TraceProgram{Copy: translations, ReaderRevision: reference, Source: result.Program.Source, Stylesheet: result.Stylesheet.Source, ViewSource: result.Template.Source}, nil
}

func acceptTraceProgram(p contract.BrowserProgram, digest string) error {
	if digest == "" || p.Resource != contract.ProgramResource || p.Digest != digest || len(p.Source) == 0 || len(p.Source) > 64<<10 || !utf8.ValidString(p.Source) || rsiplugin.FileDigest([]byte(p.Source)) != digest {
		return rsi.ErrInvalid
	}
	return nil
}

func acceptTraceStylesheet(style contract.Stylesheet, digest string) error {
	if digest == "" || style.Resource != contract.StylesheetResource || style.Digest != digest || len(style.Source) == 0 || len(style.Source) > 32<<10 || !utf8.ValidString(style.Source) || rsiplugin.FileDigest([]byte(style.Source)) != digest {
		return rsi.ErrInvalid
	}
	return nil
}

func acceptTraceTemplate(view contract.Template, digest string) error {
	if digest == "" || view.Resource != contract.TemplateResource || view.Digest != digest || len(view.Source) == 0 || len(view.Source) > 64<<10 || !utf8.ValidString(view.Source) || rsiplugin.FileDigest([]byte(view.Source)) != digest {
		return rsi.ErrInvalid
	}
	return nil
}

// TraceWorkbenchSelection freezes the whole page before reading facts. Only an
// explicitly accepted historical package without this service uses legacy UI.
func (s Inspector) TraceWorkbenchSelection(ctx context.Context, reference string) (observability.TraceWorkbench, error) {
	if !observability.ValidReaderRevision(reference) {
		return observability.TraceWorkbench{}, observability.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return observability.TraceWorkbench{}, err
	}
	defer tx.Rollback()
	host, revision, managed, err := s.traceReadSelection(ctx, tx, reference)
	if err != nil {
		return observability.TraceWorkbench{}, err
	}
	if !managed {
		return observability.TraceWorkbench{}, nil
	}
	binding, err := host.ServiceInstance(ctx, revision, contract.Contract)
	if err != nil {
		return observability.TraceWorkbench{}, err
	}
	pkg, err := host.Catalog.Load(binding.PackageDigest)
	if err != nil {
		return observability.TraceWorkbench{}, err
	}
	for _, service := range pkg.Manifest.Provides {
		if service.Contract == contract.Workbench {
			bundle, e := readTraceWorkbenchProgram(ctx, host, revision)
			if e != nil {
				return observability.TraceWorkbench{}, e
			}
			return observability.TraceWorkbench{Copy: bundle.Copy, ReaderRevision: revision, Managed: true, ViewSource: bundle.ViewSource}, nil
		}
	}
	if pkg.Manifest.PluginID != contract.PluginID || pkg.Manifest.Version != "1.0.1" {
		return observability.TraceWorkbench{}, rsi.ErrRevoked
	}
	// The old layout service checks accepted activation and the same live fences;
	// empty metadata is sufficient, with no source record or payload disclosure.
	if _, err = host.ReadService(ctx, revision, contract.Contract, json.RawMessage(`{"input_id":"observations"}`), []rsi.InputData{{ID: "observations", Data: []byte(`{"started":0,"now":1,"items":[],"timings":[]}`)}}); err != nil {
		return observability.TraceWorkbench{}, err
	}
	return observability.TraceWorkbench{ReaderRevision: revision}, nil
}

// acceptTraceCopy validates a sealed display resource, without observation facts.
func acceptTraceCopy(copy contract.Template, digest string) ([]observability.TraceTranslation, error) {
	if digest == "" || copy.Resource != contract.CopyResource || copy.Digest != digest || len(copy.Source) == 0 || len(copy.Source) > 64<<10 || !utf8.ValidString(copy.Source) || rsiplugin.FileDigest([]byte(copy.Source)) != digest {
		return nil, rsi.ErrInvalid
	}
	var translations []observability.TraceTranslation
	if err := rsiplugin.Decode(json.RawMessage(copy.Source), &translations); err != nil || len(translations) == 0 || len(translations) > 512 {
		return nil, rsi.ErrInvalid
	}
	seen := map[string]bool{}
	for _, entry := range translations {
		if entry.English == "" || entry.Chinese == "" || len(entry.English) > 2048 || len(entry.Chinese) > 4096 || seen[entry.English] {
			return nil, rsi.ErrInvalid
		}
		seen[entry.English] = true
	}
	return translations, nil
}
