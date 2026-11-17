package sqlite

import (
	"context"
	"io"
	"os"
	"path"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// uiSkillResource reads only declared Skill files in the original UI package.
// Ranking/path selection remains owned by Skill Library; file authority and
// byte integrity remain Host mechanisms. No model-supplied filesystem path is
// opened outside the sealed package root.
func (t *tools) uiSkillResource(ctx context.Context, inv tool.Invocation, name, requested string) (tool.Result, bool, error) {
	if name != "intelligent_ui" {
		return tool.Result{}, false, nil
	}
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), contract.Normalize)
	if err != nil || !managed {
		return tool.Result{}, managed, err
	}
	h, a, err := t.pureToolPolicyHost(ctx, inv)
	if err != nil {
		return tool.Result{}, true, err
	}
	binding, err := h.ServiceInstance(ctx, a.pluginGraph, contract.Normalize)
	if err != nil {
		return tool.Result{}, true, err
	}
	pkg, err := h.Catalog.Load(binding.PackageDigest)
	if err != nil {
		return tool.Result{}, true, err
	}
	// These historical package generations predate packaged Skill ownership.
	// Their frozen Runs retain the original embedded Skill reader until no such
	// Runs/references need it. New versions may never inherit this fallback.
	if uiLegacySkillPackage(pkg) {
		return tool.Result{}, false, nil
	}
	raw, err := readUISkillFile(h.Catalog, pkg, requested)
	if err == rsi.ErrNotFound {
		return tool.Failure("NOT_FOUND"), true, nil
	}
	if err != nil {
		return tool.Result{}, true, err
	}
	if err = h.Store.CheckGraph(ctx, a.pluginGraph); err != nil {
		return tool.Result{}, true, err
	}
	return tool.Result{Text: string(raw)}, true, nil
}

func uiLegacySkillPackage(pkg rsi.Package) bool {
	switch pkg.Manifest.Version {
	case "1.0.0", "1.1.0", "1.1.1", "1.2.0":
		return true
	}
	return false
}

func readUISkillFile(catalog rsi.Catalog, pkg rsi.Package, requested string) ([]byte, error) {
	if requested == "" {
		requested = "SKILL.md"
	}
	if path.Clean(requested) != requested || requested == "." || path.IsAbs(requested) {
		return nil, rsi.ErrInvalid
	}
	selected := "skill/intelligent_ui/" + requested
	count := 0
	for _, resource := range pkg.Manifest.Resources {
		if resource.Kind == "skill" && resource.Path == selected {
			count++
		}
	}
	if count == 0 {
		return nil, rsi.ErrNotFound
	}
	if count != 1 {
		return nil, rsi.ErrInvalid
	}
	digest := ""
	for _, file := range pkg.Manifest.Files {
		if file.Path == selected {
			digest = file.Digest
		}
	}
	if digest == "" {
		return nil, rsi.ErrInvalid
	}
	root, err := os.OpenRoot(catalog.Path(pkg.Digest))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(selected)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return nil, rsi.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return nil, err
	}
	if len(raw) > 65536 || rsiplugin.FileDigest(raw) != digest {
		return nil, rsi.ErrInvalid
	}
	return raw, nil
}
