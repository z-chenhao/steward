package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
)

func languageRequest(method, path, language, body string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer private-token-at-least-24-bytes")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if language != "" {
		r.AddCookie(&http.Cookie{Name: languageCookie, Value: language})
	}
	return r
}
func languageServer() *Server {
	return &Server{Config: Config{TokenRef: "credential:test"}, Core: &updatesCore{allowed: true, status: "idle"}, Secrets: fakeSecrets{}}
}
func TestLanguageDefaultsToEnglishAndPersistsExplicitChoice(t *testing.T) {
	s := languageServer()
	for _, language := range []string{"", "en", "invalid"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, languageRequest("GET", "/settings/preferences", language, ""))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `<html lang="en">`) || !strings.Contains(w.Body.String(), `<label id="language-label" for="interface-language">Language</label>`) {
			t.Fatal("default must be English, independent of browser language", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("POST", "/settings/language", "", "language=zh-CN"))
	cookies := w.Result().Cookies()
	if w.Code != 303 || w.Header().Get("Location") != "/settings/preferences?notice=language-saved" || len(cookies) != 1 || cookies[0].Value != "zh-CN" || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge <= 0 {
		t.Fatal(w.Code, w.Header())
	}
	location := w.Header().Get("Location")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("GET", location, "zh-CN", ""))
	if w.Code != 200 || w.Header().Get("Content-Language") != "zh-CN" || !strings.Contains(w.Body.String(), `<html lang="zh-CN">`) || !strings.Contains(w.Body.String(), "语言已保存。") || !strings.Contains(w.Body.String(), `type="submit">保存</button>`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("POST", "/settings/language", "zh-CN", "language=en"))
	if w.Code != 303 || w.Result().Cookies()[0].Value != "en" {
		t.Fatal("switching back failed", w.Code)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, languageRequest("POST", "/settings/language", "zh-CN", "language=es"))
	if w.Code != 400 || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), "语言无效") {
		t.Fatal("invalid choice changed preference", w.Code, w.Body.String())
	}
}
func TestLocalizedLiveRegionsPreserveContentAndApprovalIdentity(t *testing.T) {
	hash := strings.Repeat("a", 64)
	s := languageServer()
	core := s.Core.(*updatesCore)
	core.messages = []conversation.Message{{MessageID: "user-1", Sequence: 1, Role: "user", Content: conversation.Text("Ready <script>original content</script>")}}
	core.approvals = []interaction.Approval{{ID: "review-one", Status: "pending", Tool: "builtin__write", Hash: hash, Arguments: `{"path":"Ready.txt","content":"Ready"}`}}
	var etag string
	for _, language := range []string{"en", "zh-CN"} {
		w := httptest.NewRecorder()
		r := languageRequest("GET", "/api/updates?view=/", language, "")
		r.Header.Set("If-None-Match", etag)
		s.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("locale must change fragment ETag", w.Code)
		}
		var value struct{ Regions map[string]string }
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(value.Regions["conversation-messages"], "Ready &lt;script&gt;original content&lt;/script&gt;") {
			t.Fatal("business content translated or unescaped", value.Regions)
		}
		approval := value.Regions["conversation-approvals"]
		for _, immutable := range []string{`value="review-one"`, `value="` + hash + `"`, `value="approved"`, `value="denied"`, "Ready.txt", "Ready"} {
			if !strings.Contains(approval, immutable) {
				t.Fatal("approval identity or content changed", immutable, approval)
			}
		}
		if language == "zh-CN" && (!strings.Contains(approval, "确认文件修改") || !strings.Contains(approval, ">批准<") || !strings.Contains(value.Regions["workspace-status"], "就绪")) {
			t.Fatal("fragment UI remains English", value.Regions)
		}
		etag = w.Header().Get("ETag")
	}
}
func TestStaticInterfaceCopyHasChineseTranslations(t *testing.T) {
	// Explicit UI lookups only. Model/user content is deliberately not traversed.
	keys := regexp.MustCompile(`{{tr ("(?:[^"\\]|\\.)*")}}`)
	if err := fs.WalkDir(templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := templates.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range keys.FindAllStringSubmatch(string(raw), -1) {
			key, _ := strconv.Unquote(match[1])
			if _, ok := chinese[key]; !ok {
				t.Errorf("%s: missing translation %q", path, key)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if translate("zh-CN", "unknown-tool__owner") != "unknown-tool__owner" {
		t.Fatal("unknown technical identifier was rewritten")
	}
}

func TestLanguagePreferenceUsesExistingAuthenticationAndCSRFBoundary(t *testing.T) {
	s := languageServer()
	for _, accept := range []string{"text/html", "application/json"} {
		for _, authenticated := range []bool{false, true} {
			r := languageRequest("POST", "/settings/language", "", "language=zh-CN")
			r.Header.Set("Accept", accept)
			want := http.StatusUnauthorized
			if authenticated {
				r.Header.Set("Origin", "http://other.example")
				want = http.StatusForbidden
			} else {
				r.Header.Del("Authorization")
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != want || len(w.Result().Cookies()) != 0 {
				t.Fatal("language preference bypassed auth/CSRF", accept, w.Code, w.Header())
			}
		}
	}
}

func TestLanguageAutosaveJSONAndCookieReadback(t *testing.T) {
	s := languageServer()
	for _, language := range []string{"en", "zh-CN"} {
		r := languageRequest("POST", "/settings/language", "", "language="+language)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var result struct {
			Language string `json:"language"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		cookies := w.Result().Cookies()
		if w.Code != 200 || result.Language != language || w.Header().Get("Location") != "" || len(cookies) != 1 || cookies[0].Value != language || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != 365*24*60*60 {
			t.Fatal("autosave did not confirm the persisted preference", w.Code, w.Header(), w.Body.String())
		}
	}
	r := languageRequest("POST", "/settings/language", "en", "language=es")
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 400 || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), `"invalid_language"`) {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	for _, cookie := range []string{"en", "zh-CN"} {
		w = httptest.NewRecorder()
		s.ServeHTTP(w, languageRequest("GET", "/settings/preferences?notice=language-saved&language=zh-CN", cookie, ""))
		if w.Code != 200 || len(w.Result().Cookies()) != 0 {
			t.Fatal("readback must not write", w.Code, w.Header())
		}
		if cookie == "en" {
			if strings.Contains(w.Body.String(), "Language saved.") || !strings.Contains(w.Body.String(), "Check your browser cookie settings.") {
				t.Fatal("false save confirmation", w.Body.String())
			}
		} else if !strings.Contains(w.Body.String(), "语言已保存。") {
			t.Fatal("missing save confirmation", w.Body.String())
		}
	}
}
