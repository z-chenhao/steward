package sqlite

import (
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
	"testing"
)

func TestTraceProgramMustMatchDeclaredSealedBytes(t *testing.T) {
	source := "self.onmessage=()=>self.postMessage({type:'ready'});"
	digest := rsiplugin.FileDigest([]byte(source))
	good := contract.BrowserProgram{Resource: contract.ProgramResource, Digest: digest, Source: source}
	if err := acceptTraceProgram(good, digest); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*contract.BrowserProgram){
		func(p *contract.BrowserProgram) { p.Resource = "other" },
		func(p *contract.BrowserProgram) { p.Digest = rsiplugin.FileDigest([]byte("other")) },
		func(p *contract.BrowserProgram) { p.Source += "private-marker" },
		func(p *contract.BrowserProgram) { p.Source = "" },
		func(p *contract.BrowserProgram) { p.Source = string([]byte{255}) },
		func(p *contract.BrowserProgram) { p.Source = strings.Repeat("x", (64<<10)+1) },
	} {
		p := good
		change(&p)
		if acceptTraceProgram(p, digest) == nil {
			t.Fatal("unsealed program accepted")
		}
	}
	if acceptTraceProgram(good, "") == nil {
		t.Fatal("undeclared resource accepted")
	}
}

func TestTraceCopyMustMatchSealedTypedUniqueLexicon(t *testing.T) {
	good := `[{"english":"Tracing","chinese":"执行追踪"}]`
	makeCopy := func(source string) contract.Template {
		return contract.Template{Resource: contract.CopyResource, Source: source, Digest: rsiplugin.FileDigest([]byte(source))}
	}
	p := makeCopy(good)
	if got, err := acceptTraceCopy(p, p.Digest); err != nil || len(got) != 1 || got[0].Chinese != "执行追踪" {
		t.Fatal(got, err)
	}
	for _, source := range []string{
		`[]`, `null`, `{}`, `[{"english":"Tracing","chinese":""}]`,
		`[{"english":"","chinese":"追踪"}]`,
		`[{"english":"Tracing","chinese":"追踪","extra":true}]`,
		`[{"english":"Tracing","chinese":"追踪"},{"english":"Tracing","chinese":"另一版本"}]`,
		`[{"english":"` + strings.Repeat("x", 2049) + `","chinese":"追踪"}]`,
		`[{"english":"Tracing","chinese":"` + strings.Repeat("x", 4097) + `"}]`,
		strings.Repeat(" ", 65537),
	} {
		candidate := makeCopy(source)
		if _, err := acceptTraceCopy(candidate, candidate.Digest); err == nil {
			t.Fatalf("accepted invalid lexicon %q", source[:min(len(source), 80)])
		}
	}
	for _, mutate := range []func(*contract.Template){
		func(p *contract.Template) { p.Resource = "other" }, func(p *contract.Template) { p.Digest = "sha256:" + strings.Repeat("0", 64) }, func(p *contract.Template) { p.Source += " " },
	} {
		candidate := p
		mutate(&candidate)
		if _, err := acceptTraceCopy(candidate, p.Digest); err == nil {
			t.Fatal("accepted unsealed copy")
		}
	}
	if _, err := acceptTraceCopy(p, ""); err == nil {
		t.Fatal("accepted undeclared copy")
	}
}
