package web

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/observability"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func traceFixtureCopy() []observability.TraceTranslation {
	raw, err := os.ReadFile("../../plugin/traceinspector/view/copy.json")
	if err != nil {
		panic(err)
	}
	var copy []observability.TraceTranslation
	if err = json.Unmarshal(raw, &copy); err != nil {
		panic(err)
	}
	return copy
}
func ownedTraceCopy(t *testing.T) []observability.TraceTranslation {
	t.Helper()
	return traceFixtureCopy()
}
func TestTraceOwnedCopyDoesNotUseNativeDictionary(t *testing.T) {
	copy := []observability.TraceTranslation{{English: "Tracing", Chinese: "独立版本轨迹"}, {English: "Refresh", Chinese: "独立刷新"}}
	if got := traceTranslate(copy, "zh-CN", "Tracing"); got != "独立版本轨迹" {
		t.Fatal(got)
	}
	if got := traceTranslate(copy, "en", "Tracing"); got != "Tracing" {
		t.Fatal(got)
	}
	if got := traceTranslate(copy, "zh-CN", "Model cost"); got != "Model cost" {
		t.Fatal("native dictionary fallback", got)
	}
	if got := translate("zh-CN", "Refresh"); got == "独立刷新" {
		t.Fatal("shared translation changed")
	}
}

func TestTraceOwnedTemplateCannotReachSharedDictionary(t *testing.T) {
	r := httptest.NewRequest("GET", "http://localhost/developer/observability", nil)
	r.AddCookie(&http.Cookie{Name: languageCookie, Value: "zh-CN"})
	copy := []observability.TraceTranslation{{English: "Refresh", Chinese: "插件刷新"}}
	bindings := traceViewBindings(r, copy)
	if got := bindings["traceTr"].(func(string) string)("Refresh"); got != "插件刷新" {
		t.Fatal(got)
	}
	if got := bindings["tr"].(func(string) string)("Refresh"); got != translate("zh-CN", "Refresh") || got == "插件刷新" {
		t.Fatal("shared dictionary overwritten", got)
	}
	source := strings.Replace(ownedTraceTemplate(t), `{{traceTr "Tracing"}}`, `{{tr "Refresh"}}`, 1)
	if _, err := compileTraceView(source, r, copy); err == nil {
		t.Fatal("candidate reached native dictionary")
	}
}
