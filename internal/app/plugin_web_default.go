package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/config"
	extract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/internal/webcontent/parserbundle"
	"github.com/z-chenhao/assistant/internal/webcontent/parserresource"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// This initial owner source capture is separate from the candidate package.
// Existing selections never reach it; replacing installed files after selection
// cannot change a retained Run, algorithm version or archived interpreter.
func prepareWebDefault(ctx context.Context, h rsi.Host, cfg config.Config, directory string) (builtin.WebExtract, error) {
	source := webAccessConfig(cfg)
	value := builtin.WebExtract{Config: extract.Config{RuntimeRevision: extract.UnavailableRuntime, SourceIdentity: source.ParserSourceIdentity()}}
	template, err := h.Install(ctx, directory)
	if err != nil {
		return value, err
	}
	if template.Manifest.PluginID != "steward.web-extract" {
		return value, rsi.ErrInvalid
	}
	module, err := os.ReadFile(filepath.Join(h.Catalog.Path(template.Digest), extract.ModuleFile))
	if err != nil {
		return value, err
	}
	if source.ExtractCommand == "" || source.ExtractScript == "" {
		return value, nil
	}
	custom := cfg.WebAccess.ExtractScript != ""
	if custom {
		dependencies := filepath.Join(filepath.Dir(filepath.Dir(source.ExtractScript)), "node_modules")
		if info, statErr := os.Stat(dependencies); statErr != nil || !info.IsDir() {
			dependencies, err = os.MkdirTemp("", "steward-empty-parser-dependencies-")
			if err != nil {
				return value, err
			}
			defer os.RemoveAll(dependencies)
		}
		module, err = parserbundle.Bundle(ctx, source.ExtractScript, dependencies)
		if err != nil {
			if ctx.Err() != nil {
				return value, ctx.Err()
			}
			value.Config.UnavailableError = "WEB_EXTRACTION_FAILED"
			return value, nil
		}
	}
	resource, err := h.CaptureWebParser(ctx, source.ExtractCommand, module)
	if err != nil {
		if ctx.Err() != nil {
			return value, ctx.Err()
		}
		if errors.Is(err, parserresource.ErrChanged) {
			return value, err
		}
		return value, nil
	}
	value.Config.RuntimeRevision = resource.Digest()
	if !custom {
		return value, nil
	}
	// The reference is the original configured method captured before a custom
	// candidate is assembled. Candidate code cannot supply these expectations.
	for _, input := range builtin.WebExtractInputs() {
		response, err := h.WebParserReference(ctx, resource.Digest(), input)
		if ctx.Err() != nil {
			return value, ctx.Err()
		}
		if errors.Is(err, parserresource.ErrChanged) {
			return value, err
		}
		plan := extract.Plan{}
		if err != nil {
			plan.ErrorCode = "WEB_EXTRACTION_FAILED"
		} else {
			plan.TitleBytes, plan.TextBytes = len(response.Title), len(response.Text)
			plan.Digest = rsiplugin.FileDigest([]byte(response.Title + response.Text))
		}
		value.Expected = append(value.Expected, plan)
	}
	value.Module = module
	return value, nil
}
