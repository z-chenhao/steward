package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/z-chenhao/assistant/internal/memory"
)

// Experience management belongs to the trusted Reviewer control path. Ordinary
// Main/Worker capabilities still contain only list/view, never this operation.
const ManageName = "builtin__skill_manage"

type ReviewRequest struct {
	Operation       string   `json:"operation"`
	CandidateRef    string   `json:"candidate_ref,omitempty"`
	Version         int64    `json:"version,omitempty"`
	Name            string   `json:"name,omitempty"`
	Description     string   `json:"description,omitempty"`
	Procedure       string   `json:"procedure,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	SourceRefs      []string `json:"source_refs,omitempty"`
	VerificationRef string   `json:"verification_ref,omitempty"`
}

func (r ReviewRequest) Valid() bool {
	if r.Operation == "list" {
		return true
	}
	if r.Operation != "propose" {
		return strings.HasPrefix(r.CandidateRef, "experience:") && len(r.CandidateRef) <= 256 && r.Version > 0 && r.Name == "" && r.Description == "" && r.Procedure == "" && r.Reason == "" && len(r.SourceRefs) == 0 && (r.Operation == "verify" && strings.HasPrefix(r.VerificationRef, "invocation:") || r.VerificationRef == "" && (r.Operation == "view" || r.Operation == "promote" || r.Operation == "reject" || r.Operation == "retire"))
	}
	if r.Name == "" || len(r.Name) > 80 || r.Description == "" || len(r.Description) > 1024 || len(r.Procedure) < 20 || len(r.Procedure) > 8192 || len(r.SourceRefs) == 0 || len(r.SourceRefs) > 16 {
		return false
	}
	for _, c := range r.Name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	if r.Reason != "correction" && r.Reason != "recovered_failure" && r.Reason != "success" {
		return false
	}
	if memory.HasSecret(r.Procedure + "\n" + r.Description) {
		return false
	}
	for _, pattern := range []string{"Traceback (", `"tool_invocation_id"`, `"run_id"`, "--- FAIL:", "panic:", "Bearer "} {
		if strings.Contains(r.Procedure, pattern) {
			return false
		}
	}
	return strings.Contains(r.Procedure, "\n")
}
func ParseReview(raw []byte) (ReviewRequest, bool) {
	var r ReviewRequest
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || !r.Valid() {
		return r, false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return r, false
	}
	return r, true
}

func content(name, description, procedure string) []byte {
	// JSON quoted strings are valid YAML scalars; metadata cannot escape into
	// frontmatter instructions or fabricate another Skill.
	n, _ := json.Marshal(name)
	d, _ := json.Marshal(description)
	return []byte(fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", n, d, strings.TrimSpace(procedure)))
}
func Publish(ctx context.Context, c Config, id, name, description, procedure string) error {
	if c.Validate() != nil || ctx.Err() != nil {
		return errInvalid
	}
	want := content(name, description, procedure)
	front, e := parseMetadata(name, want)
	if e != nil || front.Name != name || front.Description != description {
		return errInvalid
	}
	root, close, e := openCatalog(c, true)
	if e != nil {
		return errInvalid
	}
	defer close()
	entries, e := loadRoot(root, c.Scope)
	if e != nil {
		return e
	}
	if existing, e := readFile(root, name+"/SKILL.md"); e == nil {
		if string(existing) == string(want) {
			return nil
		}
		return errInvalid
	}
	if len(entries) >= maxEntries {
		return ErrCapacity
	}
	if _, e = root.Lstat(name); !os.IsNotExist(e) {
		return errInvalid
	}
	staging := ".steward-pending-" + id
	if e = root.Mkdir(staging, 0700); e != nil && !os.IsExist(e) {
		return errInvalid
	}
	// A recoverable stage owns only SKILL.md. Reject unexpected resources or
	// links instead of publishing a directory the bounded reader cannot load.
	info, e := root.Lstat(staging)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errInvalid
	}
	staged, e := root.Open(staging)
	if e != nil {
		return errInvalid
	}
	files, readErr := staged.ReadDir(2)
	staged.Close()
	if readErr != nil && readErr != io.EOF || len(files) > 1 {
		return errInvalid
	}
	if len(files) == 1 {
		if files[0].Name() != "SKILL.md" || !files[0].Type().IsRegular() {
			return errInvalid
		}
		if e = root.Remove(staging + "/SKILL.md"); e != nil {
			return errInvalid
		}
	}
	if e = root.WriteFile(staging+"/SKILL.md", want, 0600); e != nil {
		return errInvalid
	}
	if e = root.Rename(staging, name); e != nil {
		return errInvalid
	}
	return nil
}
func Retire(c Config, id, name, description, procedure string) error {
	root, close, e := openCatalog(c, true)
	if e != nil {
		return errInvalid
	}
	defer close()
	if old, e := readFile(root, name+"/SKILL.md"); e == nil {
		if string(old) != string(content(name, description, procedure)) {
			return errInvalid
		}
		return root.Rename(name, ".steward-retired-"+id)
	}
	if old, e := readFile(root, ".steward-retired-"+id+"/SKILL.md"); e == nil && string(old) == string(content(name, description, procedure)) {
		return nil
	}
	return errInvalid
}
