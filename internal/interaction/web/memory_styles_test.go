package web

import (
	"os"
	"strings"
	"testing"
)

func TestMemoryStylesOriginalAndIndependentLayout(t *testing.T) {
	raw, e := os.ReadFile("../../plugin/memoryinspector/view/memory.css")
	if e != nil {
		t.Fatal(e)
	}
	if e = validateMemoryStylesheet(string(raw)); e != nil {
		t.Fatal("original stylesheet rejected", e)
	}
	changed := strings.Replace(string(raw), ".memory-content-section{margin-bottom:24px}", ".memory-content-section{margin-bottom:32px}", 1)
	if changed == string(raw) {
		t.Fatal("layout variant unchanged")
	}
	if e = validateMemoryStylesheet(changed); e != nil {
		t.Fatal("independent spacing rejected", e)
	}
}
func TestMemoryStylesRejectEffectsPrivateSelectorsAndHiddenFacts(t *testing.T) {
	for _, source := range []string{
		`.memory-content{font-size:0}`, `.memory-content{opacity:0}`, `.memory-content{white-space:nowrap}`, `.memory-workbench .obs-tree-pane{display:none}`, `.memory-source small{color:transparent}`, `.memory-col-content{width:0}`, `.memory-content-section{margin-bottom:-1px}`, `.memory-source>a[aria-current]{background:url('/private')}`, `.memory-source>a[aria-current]{background:u/**/rl('/private')}`, `.memory-content[data-memory-id=private]{font-size:13px}`, `body{display:none}`, `@import url('/private');`, `.memory-content{font-size:13px!important}`, `.memory-content{content:attr(data-private)}`, `.memory-content{font-size:var(--private)}`, `@container other-layout(max-width:700px){.memory-content{font-size:13px}}`, `.memory-content{font-size:13px}invalid`,
	} {
		if e := validateMemoryStylesheet(source); e == nil {
			t.Errorf("unsafe stylesheet accepted: %s", source)
		}
	}
}
