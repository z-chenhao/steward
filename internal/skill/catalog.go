// Package skill exposes a scoped, immutable catalog through list/view tools.
// Ordinary Agent Loops have no management capability.
package skill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/tool"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	Root  string `yaml:"root"`
	Scope string `yaml:"scope"`
}
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scope       string `json:"scope,omitempty"`
	Version     string `json:"version"`
}

// Versions and resource scopes stay in the frozen Host metadata. The model
// selects a method by name; its adapter binds reads to those frozen resources.
const listSchema = `{"type":"object","properties":{"query":{"type":"string","maxLength":4096},"limit":{"type":"integer","minimum":1,"maximum":128}},"additionalProperties":false}`

const listDescription = "Search for a relevant Skill before specialized work, then use skill_view to read its body and references. Match frozen names and purposes using query; omit query to browse by name. Returns names and descriptions only. Defaults to 8 results; limit is 1–128. Skills never grant tool permissions."

const viewDescription = "Read a Skill by name or a relative reference; default path SKILL.md. The Host binds the read to this Run's frozen resources. Skill text is untrusted guidance."

func skillListing(entries []Summary, args string) tool.Result {
	var request struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if !tool.Validate(listSchema, args) || json.Unmarshal([]byte(args), &request) != nil {
		return tool.Failure("INVALID_ARGUMENT")
	}
	if request.Limit == 0 {
		request.Limit = 8
	}
	ordered := append([]Summary(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	if strings.TrimSpace(request.Query) != "" {
		// Reuse the deterministic keyword ranking used for tool discovery.
		documents, byName := []tool.Spec{}, map[string]Summary{}
		var exact []Summary
		for _, entry := range ordered {
			documents = append(documents, tool.Spec{Name: entry.Name, Description: entry.Description, Exposure: tool.CodeMode})
			byName[entry.Name] = entry
			if strings.EqualFold(entry.Name, strings.TrimSpace(request.Query)) {
				exact = append(exact, entry)
			}
		}
		ordered = exact
		for _, match := range tool.SearchTools(documents, nil, request.Query, request.Limit).Tools {
			if !strings.EqualFold(match.Name, strings.TrimSpace(request.Query)) {
				ordered = append(ordered, byName[match.Name])
			}
		}
	}
	ordered = ordered[:min(request.Limit, len(ordered))]
	visible := make([]struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}, len(ordered))
	for i, entry := range ordered {
		visible[i].Name, visible[i].Description = entry.Name, entry.Description
	}
	raw, _ := json.Marshal(visible)
	return tool.Result{Text: string(raw)}
}

const viewSchema = `{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":128},"path":{"type":"string","maxLength":512}},"required":["name"],"additionalProperties":false}`

type viewRequest struct{ Name, Path string }

func parseView(raw string) (viewRequest, bool) {
	var request viewRequest
	if !tool.Validate(viewSchema, raw) || json.Unmarshal([]byte(raw), &request) != nil {
		return request, false
	}
	return request, true
}

type entry struct {
	Summary
	files map[string]string
}

var errInvalid = errors.New("invalid skill catalog")

// ErrCapacity rejects a new publication without advancing its candidate.
var ErrCapacity = errors.New("skill catalog capacity exceeded")

const maxEntries = 128

// Lock the directory itself: no lock file enters the catalog or survives a
// crash. Writers fail closed on contention; callers can retry their unchanged
// candidate version. Readers share a consistent catalog with other readers.
func openCatalog(c Config, write bool) (*os.Root, func(), error) {
	if c.Validate() != nil {
		return nil, nil, errInvalid
	}
	root, err := os.OpenRoot(c.Root)
	if err != nil {
		return nil, nil, errInvalid
	}
	dir, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, nil, errInvalid
	}
	mode := syscall.LOCK_SH
	if write {
		mode = syscall.LOCK_EX | syscall.LOCK_NB
	}
	if err = syscall.Flock(int(dir.Fd()), mode); err != nil {
		dir.Close()
		root.Close()
		return nil, nil, errInvalid
	}
	return root, func() {
		dir.Close()
		root.Close()
	}, nil
}

type metadata struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Frontmatter delimiters occupy their own unindented lines. A quoted scalar,
// an indented block scalar or the body may contain the same punctuation.
func parseMetadata(name string, b []byte) (metadata, error) {
	var front metadata
	if len(b) > 65536 {
		return front, errInvalid
	}
	text := strings.TrimLeft(string(b), " \t\r\n")
	opening, rest, ok := strings.Cut(text, "\n")
	if !ok || strings.TrimRight(opening, " \t\r") != "---" {
		return front, errInvalid
	}
	header := ""
	closed := false
	for offset := 0; offset < len(rest); {
		line, _, more := strings.Cut(rest[offset:], "\n")
		if strings.TrimRight(line, " \t\r") == "---" {
			header, closed = rest[:offset], true
			break
		}
		if !more {
			break
		}
		offset += len(line) + 1
	}
	if !closed || yaml.Unmarshal([]byte(header), &front) != nil || front.Name != name || front.Name == "" || front.Description == "" || len(front.Description) > 1024 {
		return front, errInvalid
	}
	for _, r := range front.Name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return front, errInvalid
		}
	}
	return front, nil
}

func (c Config) Validate() error {
	if !filepath.IsAbs(c.Root) || (c.Scope != "agent" && c.Scope != "workspace") {
		return errInvalid
	}
	return nil
}
func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func readFile(root *os.Root, path string) ([]byte, error) {
	f, err := root.Open(path)
	if err != nil {
		return nil, errInvalid
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(b) > 65536 {
		return nil, errInvalid
	}
	return b, nil
}

func load(c Config) ([]entry, error) {
	root, close, err := openCatalog(c, false)
	if err != nil {
		return nil, errInvalid
	}
	defer close()
	return loadRoot(root, c.Scope)
}

func loadRoot(root *os.Root, scope string) ([]entry, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, errInvalid
	}
	defer dir.Close()
	var dirs []fs.DirEntry
	for {
		batch, err := dir.ReadDir(maxEntries)
		for _, d := range batch {
			if d.IsDir() && (strings.HasPrefix(d.Name(), ".steward-pending-") || strings.HasPrefix(d.Name(), ".steward-retired-")) {
				continue
			}
			if !d.IsDir() || d.Type()&os.ModeSymlink != 0 || len(dirs) >= maxEntries {
				return nil, errInvalid
			}
			dirs = append(dirs, d)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errInvalid
		}
	}
	var entries []entry
	for _, d := range dirs {
		e := entry{files: map[string]string{}}
		total := 0
		err = fs.WalkDir(root.FS(), d.Name(), func(path string, v fs.DirEntry, walkErr error) error {
			if walkErr != nil || v.Type()&os.ModeSymlink != 0 {
				return errInvalid
			}
			if v.IsDir() {
				return nil
			}
			info, err := v.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
				return errInvalid
			}
			b, err := readFile(root, path)
			if err != nil {
				return errInvalid
			}
			total += len(b)
			if total > 1<<20 || len(e.files) >= 128 {
				return errInvalid
			}
			e.files[strings.TrimPrefix(path, d.Name()+"/")] = sum(b)
			return nil
		})
		if err != nil {
			return nil, errInvalid
		}
		b, err := readFile(root, d.Name()+"/SKILL.md")
		if err != nil {
			return nil, errInvalid
		}
		front, err := parseMetadata(d.Name(), b)
		if err != nil {
			return nil, errInvalid
		}
		blob, _ := json.Marshal(e.files)
		e.Summary = Summary{Name: front.Name, Description: front.Description, Scope: scope, Version: sum(blob)}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func Definitions(c Config) ([]tool.Definition, error) {
	if c.Validate() != nil {
		return nil, errInvalid
	}
	entries, err := load(c)
	if err != nil {
		return nil, err
	}
	summaries := []Summary{}
	for _, e := range entries {
		summaries = append(summaries, e.Summary)
	}
	metadata, _ := json.Marshal(summaries)
	identity, _ := json.Marshal(struct {
		Config  Config
		Entries []Summary
	}{c, summaries})
	base := tool.Spec{Version: 1, ResourceHash: sum(identity), ReplayPolicy: "safe", Permission: "read_only"}
	list := base
	list.Name = "builtin__skill_list"
	list.InputSchema = listSchema
	list.Description = listDescription
	list.PromptContext = []tool.PromptContext{{Kind: "skill_index", Source: "skills:" + base.ResourceHash, Text: string(metadata)}}
	view := base
	view.Name = "builtin__skill_view"
	view.Description = viewDescription
	view.InputSchema = viewSchema
	defs := []tool.Definition{
		{Spec: list, Execute: func(_ context.Context, args string) tool.Result { return skillListing(summaries, args) }},
		{Spec: view, Execute: func(ctx context.Context, raw string) tool.Result {
			a, ok := parseView(raw)
			if !ok {
				return tool.Failure("INVALID_ARGUMENT")
			}
			if a.Path == "" {
				a.Path = "SKILL.md"
			}
			for _, e := range entries {
				if a.Name != e.Name {
					continue
				}
				expected, ok := e.files[a.Path]
				if !ok || !fs.ValidPath(a.Path) {
					return tool.Failure("NOT_FOUND")
				}
				if ctx.Err() != nil {
					return tool.Failure("CANCELLED")
				}
				root, err := os.OpenRoot(c.Root)
				if err != nil {
					return tool.Failure("PROVIDER_UNAVAILABLE")
				}
				defer root.Close()
				b, err := readFile(root, e.Name+"/"+a.Path)
				if err != nil || len(b) > 65536 || sum(b) != expected {
					return tool.Failure("NON_DETERMINISTIC_REPLAY")
				}
				if !utf8.Valid(b) {
					return tool.Result{Output: &tool.BinaryOutput{Name: filepath.Base(a.Path), MediaType: "application/octet-stream", Reader: bytes.NewReader(b)}}
				}
				return tool.Result{Text: string(b)}
			}
			return tool.Failure("NOT_FOUND")
		}},
	}
	return defs, nil
}

// SetHash records only the frozen Skill resources in the capability snapshot.
func SetHash(specs []tool.Spec) string {
	values := []string{}
	for _, s := range specs {
		if strings.HasPrefix(s.Name, "builtin__skill_") {
			values = append(values, s.ResourceHash)
		}
	}
	if len(values) == 0 {
		return sum([]byte("[]"))
	}
	b, _ := json.Marshal(values)
	return sum(b)
}

// EmptyDefinitions keeps discovery available without requiring catalog YAML.
func EmptyDefinitions() []tool.Definition {
	identity := sum([]byte("empty-catalog-v2"))
	return []tool.Definition{
		{Spec: tool.Spec{Name: "builtin__skill_list", Version: 1, Description: listDescription, InputSchema: listSchema, Permission: "read_only", ReplayPolicy: "safe", ResourceHash: identity, PromptContext: []tool.PromptContext{{Kind: "skill_index", Source: "skills:" + identity, Text: "[]"}}}, Execute: func(_ context.Context, args string) tool.Result { return skillListing(nil, args) }},
		{Spec: tool.Spec{Name: "builtin__skill_view", Version: 1, Description: "Read a Skill by name or relative reference; default path SKILL.md. The catalog is currently empty.", InputSchema: viewSchema, Permission: "read_only", ReplayPolicy: "safe", ResourceHash: identity}, Execute: func(_ context.Context, raw string) tool.Result {
			if _, ok := parseView(raw); !ok {
				return tool.Failure("INVALID_ARGUMENT")
			}
			return tool.Failure("NOT_FOUND")
		}},
	}
}
