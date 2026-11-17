package web

import (
	"strings"
	"testing"
)

func TestWorkbenchCapturedStylesheetIsPassive(t *testing.T) {
	p := originalWorkbenchPresentation(t)
	if err := validateWorkbenchStylesheet(p.Stylesheet.Source); err != nil {
		t.Fatal(err)
	}
}

func TestWorkbenchStylesRejectNetworkPrivateSelectorsAndEscapes(t *testing.T) {
	original := originalWorkbenchPresentation(t).Stylesheet.Source
	for _, source := range []string{
		original + `.task-rule-name{background:url(/private)}`,
		original + `[data-secret]{display:none}`,
		original + `.task-rule-name{color:expression(alert(1))}`,
		original + `@import "/private";`,
		original + `.task-rule-name{content:attr(data-private)}`,
		original + `.task-rule-name{background:u\72l(/private)}`,
	} {
		if validateWorkbenchStylesheet(source) == nil {
			t.Fatal("unsafe candidate stylesheet admitted")
		}
	}
	changed := strings.Replace(original, "font-size:15px;font-weight:550", "font-size:17px;font-weight:550", 1)
	if err := validateWorkbenchStylesheet(changed); err != nil {
		t.Fatal("bounded independent typography iteration rejected", err)
	}
}
