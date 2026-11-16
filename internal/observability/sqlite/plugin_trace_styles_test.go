package sqlite

import (
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
	"testing"
)

func TestTraceStylesheetAcceptsOnlyExactSealedBytes(t *testing.T) {
	source := ".obs-activity-chart{height:96px}"
	digest := rsiplugin.FileDigest([]byte(source))
	valid := contract.Stylesheet{Resource: contract.StylesheetResource, Digest: digest, Source: source}
	if acceptTraceStylesheet(valid, digest) != nil {
		t.Fatal("sealed resource rejected")
	}
	for _, change := range []func(*contract.Stylesheet){func(p *contract.Stylesheet) { p.Source += " " }, func(p *contract.Stylesheet) { p.Resource = "foreign" }, func(p *contract.Stylesheet) { p.Digest = "foreign" }, func(p *contract.Stylesheet) { p.Source = "" }, func(p *contract.Stylesheet) { p.Source = string([]byte{255}) }, func(p *contract.Stylesheet) {
		p.Source = strings.Repeat("x", (32<<10)+1)
		p.Digest = rsiplugin.FileDigest([]byte(p.Source))
	}} {
		value := valid
		change(&value)
		if acceptTraceStylesheet(value, digest) == nil {
			t.Fatal("unbound resource accepted")
		}
	}
	if acceptTraceStylesheet(valid, "") == nil {
		t.Fatal("undeclared resource accepted")
	}
}

func TestTraceTemplateAcceptsOnlyExactSealedResource(t *testing.T) {
	source := `{{define "observability-page"}}<section></section>{{end}}`
	digest := rsiplugin.FileDigest([]byte(source))
	valid := contract.Template{Resource: contract.TemplateResource, Digest: digest, Source: source}
	if acceptTraceTemplate(valid, digest) != nil {
		t.Fatal("sealed template rejected")
	}
	for _, change := range []func(*contract.Template){func(p *contract.Template) { p.Source += " " }, func(p *contract.Template) { p.Resource = "foreign" }, func(p *contract.Template) { p.Digest = "foreign" }, func(p *contract.Template) { p.Source = "" }, func(p *contract.Template) {
		p.Source = string([]byte{255})
		p.Digest = rsiplugin.FileDigest([]byte(p.Source))
	}, func(p *contract.Template) {
		p.Source = strings.Repeat("x", (64<<10)+1)
		p.Digest = rsiplugin.FileDigest([]byte(p.Source))
	}} {
		value := valid
		change(&value)
		if acceptTraceTemplate(value, digest) == nil {
			t.Fatal("unbound template accepted")
		}
	}
	if acceptTraceTemplate(valid, "") == nil {
		t.Fatal("undeclared template accepted")
	}
}
