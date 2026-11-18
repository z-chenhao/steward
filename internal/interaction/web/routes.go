package web

import (
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
)

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/settings/models/new" && r.URL.Query().Get("provider") == "chatgpt" {
		http.Redirect(w, r, "/settings/models", http.StatusSeeOther)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/apps/logo/") {
		s.appLogo(w, r)
		return
	}
	if legacyPageRedirect(w, r) {
		return
	}
	if _, view, _ := pageRoute(r.URL.Path); view != "" {
		s.show(w, r)
		return
	}
	switch r.URL.Path {
	case "/api/workbench/style":
		s.workbenchStylesheet(w, r)
	case "/api/models":
		s.composerModelsAPI(w, r)
	case "/api/ui/state", "/api/ui/action":
		s.uiInteraction(w, r)
	case "/ui/image":
		s.uiImage(w, r)

	case "/api/developer/memory/program":
		s.memoryProgram(w, r)
	case "/api/developer/memory/style":
		s.memoryStylesheet(w, r)
	case "/api/apps/browser":
		s.appBrowser(w, r)
	case "/api/apps/detail":
		s.appDetail(w, r)
	case "/ui/preview":
		s.previewWidget(w, r)
	case "/ui/widget":
		s.widget(w, r)
	case "/ui/plan-preview":
		s.planPreview(w, r)
	case "/ui/plan":
		s.planView(w, r)
	case "/ui/table-preview":
		s.tablePreview(w, r)
	case "/ui/cards-preview":
		s.cardsPreview(w, r)
	case "/ui/chart-preview":
		s.chartPreview(w, r)
	case "/ui/form-view":
		s.formView(w, r)
	case "/ui/chart":
		s.chartView(w, r)
	case "/ui/table":
		s.tableView(w, r)
	case "/ui/cards":
		s.cardsView(w, r)
	case "/ui/cards-link":
		s.cardsLink(w, r)
	case "/ui/cards-image":
		s.cardsImage(w, r)
	case "/api/developer/memory":
		s.memoryAPI(w, r)
	case "/api/workers":
		s.workerView(w, r, false)
	case "/api/workers/current":
		s.workerView(w, r, true)
	case "/api/computer/status":
		s.computerView(w, r, false)
	case "/api/computer/frame":
		s.computerView(w, r, true)
	case "/api/developer/traces/style":
		s.traceStylesheet(w, r)
	case "/trace/legacy-activity-v1.css":
		s.legacyTraceStylesheet(w, r)
	case "/api/developer/traces/program":
		s.traceProgram(w, r)
	case "/api/developer/traces":
		s.traceAPI(w, r, "traces")
	case "/api/developer/observations":
		s.traceAPI(w, r, "observations")
	case "/api/developer/observation-body":
		s.traceAPI(w, r, "body")
	case "/api/developer/rounds":
		s.contextAPI(w, r, "rounds")
	case "/api/developer/context":
		s.contextAPI(w, r, "context")
	case "/api/developer/context-diff":
		s.contextAPI(w, r, "diff")
	case "/api/developer/executions":
		s.inspectionAPI(w, r, false)
	case "/api/developer/relationships":
		s.inspectionAPI(w, r, true)
	case "/api/updates":
		s.updates(w, r)
	case "/api/conversation/stream":
		s.streamReplies(w, r)
	case "/api/conversation":
		before, e := conversationCursor(r)
		if e != nil {
			fail(w, 400)
			return
		}
		p, e := s.Core.Conversation(r.Context(), before)
		if e != nil {
			fail(w, 503)
			return
		}
		if p.Before != 0 {
			w.Header().Set("X-Steward-Before", strconv.FormatInt(p.Before, 10))
		}
		jsonResult(w, p.Messages)
	case "/api/progress":
		a, e := s.Core.Activity(r.Context())
		if e != nil {
			fail(w, 503)
			return
		}
		jsonResult(w, a)
	case "/api/status":
		a, e := s.Core.WebStatus(r.Context())
		if e != nil {
			fail(w, 503)
			return
		}
		jsonResult(w, a)
	case "/api/auth":
		s.mu.Lock()
		defer s.mu.Unlock()
		jsonResult(w, struct {
			llm.AuthState
			AccountID string `json:"account_id,omitempty"`
			Error     string `json:"error,omitempty"`
		}{s.auth, s.accountID, s.authError})
	case "/artifact", "/artifact/preview":
		if r.URL.Path == "/artifact/preview" && !s.artifactPreviewAllowed(r) {
			fail(w, 404)
			return
		}
		ref := r.URL.Query().Get("ref")
		d, e := s.Core.DescribeArtifact(r.Context(), ref)
		if e != nil {
			fail(w, 404)
			return
		}
		src, e := s.Core.OpenArtifact(r.Context(), ref)
		if e != nil {
			fail(w, 404)
			return
		}
		defer src.Close()
		if r.URL.Path == "/artifact/preview" {
			serveArtifactPreview(w, d, src)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Name}))
		_, _ = io.Copy(w, src)
	default:
		fail(w, 404)
	}
}
func (s *Server) post(w http.ResponseWriter, r *http.Request) error {
	switch r.URL.Path {
	case "/api/models/project":
		s.composerModelProjection(w, r)
		return nil
	case "/api/ui/state", "/api/ui/action":
		s.uiInteraction(w, r)
		return nil
	case "/api/message":
		return s.acceptJSONMessage(w, r)
	case "/message":
		return s.acceptFormMessage(w, r)
	case "/ui/form":
		return s.respondUIForm(w, r)
	case "/upload":
		return s.uploadArtifact(w, r)
	}
	if r.URL.Path == "/capability/configure" && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 10); err != nil {
			return err
		}
		defer r.MultipartForm.RemoveAll()
	} else if e := r.ParseForm(); e != nil {
		return e
	}
	switch r.URL.Path {
	case "/task/feedback":
		return s.respondFeedback(w, r)
	case "/schedule/trial":
		return s.trialSchedule(w, r)
	case "/schedule/configure":
		return s.configureSchedule(w, r)
	case "/settings/language":
		return s.setLanguage(w, r)
	case "/capability/configure":
		return s.capabilityAction(w, r)
	case "/apps/configure":
		return s.appAction(w, r)
	case "/apps/service/configure":
		return s.appServiceAction(w, r)
	case "/approval":
		if err := s.decideApproval(r); err != nil {
			return err
		}
		if r.Header.Get("Accept") == "application/json" {
			jsonResult(w, struct {
				Accepted bool `json:"accepted"`
			}{true})
			return nil
		}
		http.Redirect(w, r, safePageReturn(r.FormValue("return_to")), http.StatusSeeOther)
		return nil
	case "/model/choose":
		return s.composerModelAction(w, r)
	case "/model/reconnect":
		return s.reconnectModel(w, r)
	case "/model/disconnect":
		return s.disconnectKeyModel(w, r)
	case "/model/add":
		status, err := s.Core.WebStatus(r.Context())
		if err != nil {
			return err
		}
		p, action, err := s.addModel(r, status)
		if err != nil {
			return err
		}
		notice := "added"
		switch action {
		case "activate":
			err = s.Control.SelectWithProbe(r.Context(), p.ProfileID, p.Version, status.ConfigVersion)
			if err == nil {
				err = s.Core.WaitForRuntime(r.Context())
			}
			notice = "active"
		case "probe":
			_, err = s.Control.Test(r.Context(), p.ProfileID)
		}
		if err != nil {
			// The candidate is already saved. Retain its resource and recovery entry,
			// rather than offering to create the same model again.
			next := r.Clone(r.Context())
			u := *r.URL
			u.Path = "/model/select"
			next.URL = &u
			next.PostForm = url.Values{"profile": {p.ProfileID}}
			next.Form = next.PostForm
			s.postFailure(w, next, err)
			return nil
		}
		http.Redirect(w, r, resourceModelURL(p.ProfileID, notice), http.StatusSeeOther)
		return nil
	case "/model/test":
		if e := s.testModel(r); e != nil {
			return e
		}
		http.Redirect(w, r, resourceModelURL(r.FormValue("profile"), "tested"), http.StatusSeeOther)
		return nil
	case "/model/select":
		if err := s.selectModel(r); err != nil {
			return err
		}
		http.Redirect(w, r, resourceModelURL(r.FormValue("profile"), "active"), http.StatusSeeOther)
		return nil
	case "/notification":
		return s.notificationAction(w, r)
	case "/auth/login":
		if err := s.startLogin(r); err != nil {
			return err
		}
	case "/auth/logout":
		if s.Accounts == nil {
			return llm.ErrNoBridge
		}
		if _, err := s.Accounts.Logout(r.Context(), r.FormValue("account")); err != nil {
			return err
		}
		s.mu.Lock()
		if s.accountID == r.FormValue("account") {
			s.auth = llm.AuthState{}
			s.authOnResources = false
			s.authError = ""
			s.accountID = ""
		}
		s.mu.Unlock()
	case "/auth/cancel":
		s.mu.Lock()
		cancel := s.authCancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	default:
		fail(w, 404)
		return nil
	}
	target := "/"
	if strings.HasPrefix(r.URL.Path, "/auth/") {
		target = accountReturn(r)
	}
	http.Redirect(w, r, safePageReturn(target), http.StatusSeeOther)
	return nil
}
