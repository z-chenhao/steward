// Package rsi hosts versioned feature plugins. It owns composition and process
// lifecycle; agent and tool domains still own their admission and persistence.
package rsi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

var (
	ErrInvalid      = errors.New("RSI_INVALID")
	ErrConflict     = errors.New("RSI_CONFLICT")
	ErrRevoked      = errors.New("RSI_REVOKED")
	ErrUnknown      = errors.New("RSI_RESULT_UNKNOWN")
	ErrNotFound     = errors.New("RSI_NOT_FOUND")
	ErrVerification = errors.New("RSI_VERIFICATION_FAILED")
)
var identifier = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
var version = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$`)
var contentDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Package struct {
	Digest   string             `json:"digest"`
	Manifest rsiplugin.Manifest `json:"manifest"`
}
type Catalog struct {
	Root string
	// Launcher is an explicit trusted test carrier. Production leaves it empty;
	// manifests, SDK requests and environment cannot override the Host binary.
	Launcher string
}

func validPath(s string) bool {
	return s != "" && fs.ValidPath(s) && s != "." && !strings.ContainsAny(s, "\\\x00\r\n")
}

func readBounded(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrInvalid
	}
	// The regular-file size is already bounded. Allocate once instead of
	// repeatedly growing ReadAll buffers for every pre/post execution hash.
	raw := make([]byte, int(info.Size()))
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, err
	}
	var extra [1]byte
	if n, err := f.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, ErrInvalid
	}
	return raw, nil
}
func ValidateManifest(m rsiplugin.Manifest) error {
	if m.Protocol != rsiplugin.Protocol || !identifier.MatchString(m.PluginID) || !version.MatchString(m.Version) || !validPath(m.Runtime.Entry) || m.Runtime.TimeoutMS < 1 || m.Runtime.TimeoutMS > 30000 || len(m.Files) == 0 || len(m.Files) > 256 {
		return ErrInvalid
	}
	requests := map[string]bool{}
	for _, request := range m.Requests {
		if requests[request] {
			return ErrInvalid
		}
		requests[request] = true
		switch request {
		case "run.context.read", "run.tool.read", "run.reply.read", "state.run.write", "evolution.propose", "input.read", "compute.ocr", "compute.web-extract", "resource.call":
		default:
			return ErrInvalid
		}
	}
	if !tool.SchemaSupported(string(m.ConfigSchema)) || !tool.SchemaSupported(string(m.State.Schema)) || m.State.Version < 1 || !tool.Validate(string(m.State.Schema), string(m.State.Initial)) {
		return ErrInvalid
	}
	files := map[string]rsiplugin.File{}
	for _, f := range m.Files {
		if !validPath(f.Path) || f.Path == "steward.plugin.json" || !contentDigest.MatchString(f.Digest) {
			return ErrInvalid
		}
		if _, ok := files[f.Path]; ok {
			return ErrInvalid
		}
		files[f.Path] = f
	}
	if !files[m.Runtime.Entry].Executable {
		return ErrInvalid
	}
	contracts := map[string]bool{}
	for _, s := range m.Provides {
		if !validContract(s.Contract) || contracts[s.Contract] || !tool.SchemaSupported(string(s.InputSchema)) || !tool.SchemaSupported(string(s.OutputSchema)) {
			return ErrInvalid
		}
		contracts[s.Contract] = true
	}
	if len(m.Provides) > 64 || len(m.Hooks) > 32 || len(m.Tools) > 64 || len(m.Requires) > 32 || len(m.Resources) > 128 {
		return ErrInvalid
	}
	hooks := map[rsiplugin.Stage]bool{}
	for _, h := range m.Hooks {
		if !h.Stage.Valid() || hooks[h.Stage] {
			return ErrInvalid
		}
		hooks[h.Stage] = true
	}
	names := map[string]bool{}
	for _, t := range m.Tools {
		if !strings.HasPrefix(t.Name, "plugin__") || !identifier.MatchString(t.Name) || names[t.Name] || !contracts[t.Contract] || len(t.Description) > 8192 {
			return ErrInvalid
		}
		names[t.Name] = true
	}
	for _, r := range m.Resources {
		key := r.Kind + ":" + r.Name
		if !identifier.MatchString(r.Name) || names[key] || files[r.Path].Path == "" {
			return ErrInvalid
		}
		switch r.Kind {
		case "skill", "prompt", "view", "settings":
		default:
			return ErrInvalid
		}
		names[key] = true
	}
	names = map[string]bool{}
	for _, r := range m.Requires {
		if !identifier.MatchString(r.Binding) || names[r.Binding] || !validContract(r.Contract) {
			return ErrInvalid
		}
		names[r.Binding] = true
	}
	return nil
}
func validContract(s string) bool {
	name, v, ok := strings.Cut(s, "/")
	return ok && identifier.MatchString(name) && len(v) > 0 && len(v) < 8 && strings.IndexFunc(v, func(r rune) bool { return r < '0' || r > '9' }) < 0 && v[0] != '0'
}

// Install copies declared bytes before publishing a content-addressed package.
// os.Root prevents source path traversal even when untrusted input is changing.
func (c Catalog) Install(ctx context.Context, source string) (Package, error) {
	var result Package
	root, err := os.OpenRoot(source)
	if err != nil {
		return result, err
	}
	defer root.Close()
	raw, err := readBounded(root, "steward.plugin.json", rsiplugin.MaxFrameBytes)
	if err != nil {
		return result, err
	}
	if err = rsiplugin.Decode(raw, &result.Manifest); err != nil {
		return result, ErrInvalid
	}
	if err = ValidateManifest(result.Manifest); err != nil {
		return result, err
	}
	result.Digest, err = rsiplugin.Digest("steward.rsi.package/1", result.Manifest)
	if err != nil {
		return result, ErrInvalid
	}
	if err = os.MkdirAll(c.Root, 0700); err != nil {
		return result, err
	}
	tmp, err := os.MkdirTemp(c.Root, ".install-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmp)
	declared := map[string]bool{"steward.plugin.json": true}
	for _, f := range result.Manifest.Files {
		declared[f.Path] = true
	}
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if !d.IsDir() && !declared[name] {
			return ErrInvalid
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	total := int64(0)
	for _, f := range result.Manifest.Files {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		info, e := root.Lstat(f.Path)
		if e != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
			return result, ErrInvalid
		}
		b, e := readBounded(root, f.Path, 32<<20)
		if e != nil {
			return result, e
		}
		total += int64(len(b))
		if total > 64<<20 || rsiplugin.FileDigest(b) != f.Digest {
			return result, ErrInvalid
		}
		dest := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return result, err
		}
		mode := os.FileMode(0400)
		if f.Executable {
			mode = 0500
		}
		if err = os.WriteFile(dest, b, mode); err != nil {
			return result, err
		}
	}
	encoded, _ := rsiplugin.Canonical(result.Manifest)
	if err = os.WriteFile(filepath.Join(tmp, "steward.plugin.json"), encoded, 0400); err != nil {
		return result, err
	}
	dest := c.Path(result.Digest)
	if _, err = os.Stat(dest); err == nil {
		return c.Load(result.Digest)
	}
	if err = os.Rename(tmp, dest); err != nil {
		if old, e := c.Load(result.Digest); e == nil {
			return old, nil
		}
		return result, err
	}
	return result, nil
}
func (c Catalog) Path(digest string) string {
	return filepath.Join(c.Root, strings.TrimPrefix(digest, "sha256:"))
}
func (c Catalog) Load(digest string) (Package, error) {
	var p Package
	if !contentDigest.MatchString(digest) {
		return p, ErrInvalid
	}
	root, err := os.OpenRoot(c.Path(digest))
	if err != nil {
		return p, ErrNotFound
	}
	defer root.Close()
	b, err := readBounded(root, "steward.plugin.json", rsiplugin.MaxFrameBytes)
	if err != nil {
		return p, err
	}
	if rsiplugin.Decode(b, &p.Manifest) != nil || ValidateManifest(p.Manifest) != nil {
		return p, ErrInvalid
	}
	p.Digest, err = rsiplugin.Digest("steward.rsi.package/1", p.Manifest)
	if err != nil || p.Digest != digest {
		return p, ErrInvalid
	}
	for _, f := range p.Manifest.Files {
		info, e := root.Lstat(f.Path)
		if e != nil || !info.Mode().IsRegular() {
			return p, ErrInvalid
		}
		data, e := readBounded(root, f.Path, 32<<20)
		if e != nil || rsiplugin.FileDigest(data) != f.Digest {
			return p, ErrInvalid
		}
	}
	return p, nil
}

// Seal creates a manifest file inventory for an explicitly built local package.
// It never runs build or installation scripts from that package.
func Seal(directory string, m rsiplugin.Manifest) (rsiplugin.Manifest, error) {
	m.Files = nil
	err := filepath.WalkDir(directory, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(directory, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if rel == "steward.plugin.json" {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		m.Files = append(m.Files, rsiplugin.File{Path: rel, Digest: rsiplugin.FileDigest(b), Executable: info.Mode().Perm()&0111 != 0})
		return nil
	})
	if err != nil {
		return m, err
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m, ValidateManifest(m)
}

type ResolvedGraph struct {
	Revision string          `json:"revision"`
	Graph    rsiplugin.Graph `json:"graph"`
	Order    []string        `json:"order"`
}

func (c Catalog) Resolve(g rsiplugin.Graph) (ResolvedGraph, error) {
	var out ResolvedGraph
	if g.Protocol != rsiplugin.Protocol || len(g.Bindings) > 64 {
		return out, ErrInvalid
	}
	g.Bindings = append([]rsiplugin.Binding(nil), g.Bindings...)
	sort.Slice(g.Bindings, func(i, j int) bool { return g.Bindings[i].InstanceID < g.Bindings[j].InstanceID })
	bindings := map[string]rsiplugin.Binding{}
	packages := map[string]Package{}
	tools := map[string]bool{}
	namespaces := map[string]int{}
	for i, b := range g.Bindings {
		if !identifier.MatchString(b.InstanceID) || !identifier.MatchString(b.StateNamespace) {
			return out, ErrInvalid
		}
		if _, ok := bindings[b.InstanceID]; ok {
			return out, ErrInvalid
		}
		p, err := c.Load(b.PackageDigest)
		if err != nil {
			return out, err
		}
		if b.StateVersion != p.Manifest.State.Version || !tool.Validate(string(p.Manifest.ConfigSchema), string(b.Config)) {
			return out, ErrInvalid
		}
		if _, ok := namespaces[b.StateNamespace]; ok {
			return out, ErrInvalid
		}
		namespaces[b.StateNamespace] = b.StateVersion
		for _, t := range p.Manifest.Tools {
			if tools[t.Name] {
				return out, ErrInvalid
			}
			tools[t.Name] = true
		}
		b.Dependencies = append([]rsiplugin.Dependency(nil), b.Dependencies...)
		sort.Slice(b.Dependencies, func(i, j int) bool { return b.Dependencies[i].Binding < b.Dependencies[j].Binding })
		g.Bindings[i] = b
		bindings[b.InstanceID] = b
		packages[b.InstanceID] = p
	}
	visited := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] == 1 {
			return ErrInvalid
		}
		if visited[id] == 2 {
			return nil
		}
		visited[id] = 1
		b := bindings[id]
		p := packages[id]
		if len(b.Dependencies) != len(p.Manifest.Requires) {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, d := range b.Dependencies {
			if seen[d.Binding] {
				return ErrInvalid
			}
			seen[d.Binding] = true
			target, ok := packages[d.InstanceID]
			if !ok {
				return ErrInvalid
			}
			matched := false
			for _, r := range p.Manifest.Requires {
				matched = matched || r.Binding == d.Binding && r.Contract == d.Contract
			}
			provided := false
			for _, s := range target.Manifest.Provides {
				provided = provided || s.Contract == d.Contract
			}
			if !matched || !provided {
				return fmt.Errorf("%w: dependency %s", ErrInvalid, d.Binding)
			}
			if err := visit(d.InstanceID); err != nil {
				return err
			}
		}
		visited[id] = 2
		out.Order = append(out.Order, id)
		return nil
	}
	for _, b := range g.Bindings {
		if err := visit(b.InstanceID); err != nil {
			return out, err
		}
	}
	var err error
	out.Revision, err = rsiplugin.Digest("steward.rsi.graph/1", g)
	out.Graph = g
	return out, err
}

func resourcePath(packageRoot, relative string) string {
	return filepath.Join(packageRoot, filepath.FromSlash(path.Clean(relative)))
}
