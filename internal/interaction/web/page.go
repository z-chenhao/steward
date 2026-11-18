package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/llm"
)

type shownMessage struct {
	Role         string
	Sequence     int64
	ReplyKey     string
	ClientSource string
	CreatedAt    string
	Bubbles      []shownProjection
	Projection   shownProjection
	Approval     *approvalView
}
type shownProjection struct {
	Key            string
	HTML           template.HTML
	Spec, Fallback string
}
type page struct {
	Workbench                                         *workbenchPresentation
	WorkbenchUnavailable                              bool
	CapabilityView                                    *capabilityViewAssets
	ComposerModels                                    composerModels
	Apps                                              *appsView
	Capabilities                                      []Capability
	Capability                                        *Capability
	Trace                                             *traceView
	Memory                                            *memoryView
	Tasks                                             *taskView
	Attention                                         *attentionView
	FeedbackDraft                                     string
	FormDraft                                         []ui.Field
	Schedule                                          *scheduleEditor
	Settings                                          *settingsView
	Accounts                                          []llm.AuthConnection
	Bindings                                          []interaction.Binding
	Preference                                        interaction.Preference
	Language                                          string
	Login                                             bool
	Tab, View, Title, Path, Next, Error, Notice, Live string
	Status                                            Status
	Cursor, Latest, Before                            int64
	Messages                                          []shownMessage
	Approvals                                         []approvalView
	PendingCount                                      int
	FirstPending                                      string
	SourceID                                          string
	Auth                                              llm.AuthState
	AuthError                                         string
	AuthOnResources                                   bool
	AccountID                                         string
	Draft                                             string
	DraftTask                                         *taskContext
}
type taskContext struct{ Ref, Title string }

func sourceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
func (s *Server) page(ctx context.Context, r *http.Request) (page, error) {
	tab, view, title := pageRoute(r.URL.Path)
	p := page{Tab: tab, View: view, Title: title, Path: r.URL.Path, SourceID: sourceID(), Notice: notice(r.URL.Query().Get("notice"))}
	if view == "" {
		return p, errPageNotFound
	}
	var err error
	// Read the public owner recipe before any authorized page facts. No slow
	// plugin computation is inserted between reading private facts and release.
	if err = s.loadWorkbench(ctx, r, &p); err != nil {
		return p, err
	}
	p.Live = workbenchLivePath(p, r)
	p.Status, err = s.Core.WebStatus(ctx)
	if err != nil {
		return p, err
	}
	switch p.View {
	case "app-service":
		ui := s.appInterface(ctx, r.URL.Query().Get("interface_revision"), requestLanguage(r))
		err = s.loadAppService(context.WithValue(ctx, appInterfaceKey{}, ui), r, &p)
	case "apps":
		err = s.loadApps(ctx, r, &p)
	case "memory":
		p.Memory, err = s.loadMemory(ctx, r)
	case "schedule":
		err = s.loadSchedule(ctx, r, &p)
	case "capabilities":
		err = s.loadCapabilities(ctx, r, &p)
	case "settings":
		s.loadSignIn(&p, r.URL.Path)
		err = s.loadSettings(ctx, r, &p)
	case "tasks", "task", "execution", "attention":
		err = s.loadTasks(ctx, r, &p)
	case "traces", "trace":
		p.Trace, err = tracePageQuery(r)
		if err == nil {
			err = s.loadTrace(ctx, p.Trace)
		}
		if p.Trace != nil && p.Trace.Run != "" {
			p.View = "trace"
		}
	case "conversation":
		p.Cursor, err = conversationCursor(r)
		if err != nil {
			return p, err
		}
		if ref := r.URL.Query().Get("task"); ref != "" {
			if s.Tasks == nil {
				return p, errPageNotFound
			}
			d, e := s.Tasks.Detail(ctx, ref, "")
			if e != nil {
				return p, e
			}
			p.DraftTask = &taskContext{Ref: d.TaskRef, Title: d.Title}
		}
		if r.URL.Query().Has("correct_memory") {
			p.Draft, err = s.memoryCorrectionDraft(ctx, r)
			if err != nil {
				return p, err
			}
		}
		err = s.loadConversation(ctx, &p)
		if err == nil {
			p.ComposerModels, err = s.modelChoices(ctx, false)
		}
	}
	return p, err
}
func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	p, err := s.page(r.Context(), r)
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	if p.Settings != nil && p.Settings.Section == "models" && strings.HasPrefix(r.URL.Path, "/settings/models/") {
		http.Redirect(w, r, resourceModelURL(p.Settings.TargetModel, r.URL.Query().Get("notice")), http.StatusSeeOther)
		return
	}
	s.render(w, r, p)
}
func (s *Server) render(w http.ResponseWriter, r *http.Request, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	p.Language = requestLanguage(r)
	// The autosave navigation carries the intended value. Read the browser's
	// cookie back before presenting success (cookies may be blocked).
	if p.Path == "/settings/preferences" && p.Notice == "Language saved." {
		if expected := r.URL.Query().Get("language"); expected != "" && expected != p.Language {
			p.Notice = ""
			p.Error = "Language could not be retained. Check your browser cookie settings."
		}
	}
	w.Header().Set("Content-Language", p.Language)
	if p.Workbench != nil {
		w.Header().Set("X-Steward-Workbench-Reader", p.Workbench.ReaderRevision)
	}
	if p.View == "capabilities" {
		fragment, err := renderCapabilityView(p, r)
		if err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		outer := requestLayout(r).Funcs(template.FuncMap{"capabilityPageView": func() template.HTML { return fragment }})
		if _, err = outer.New("capabilities-page").Parse(`{{capabilityPageView}}`); err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		var body bytes.Buffer
		if err = outer.Execute(&body, p); err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(body.Bytes())
		return
	}

	if p.Settings != nil && p.Settings.View != nil {
		name := "model-list"
		if p.Settings.Section == "new" {
			name = "model-new"
		}
		fragment, err := renderModelView(p, r, name)
		if err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		outer := requestLayout(r).Funcs(template.FuncMap{"modelConnectionsView": func() template.HTML { return fragment }})
		if _, err = outer.New(name).Parse(`{{modelConnectionsView}}`); err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		var body bytes.Buffer
		if err = outer.Execute(&body, p); err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(body.Bytes())
		return
	}
	if p.Trace != nil && p.Trace.WorkbenchManaged {
		outer, err := tracePageLayout(r, p.Trace)
		if err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		var body bytes.Buffer
		if err = outer.Execute(&body, p); err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(body.Bytes())
		return
	}
	if p.Memory != nil && p.Memory.Presentation != nil {
		if validateMemoryStylesheet(p.Memory.Presentation.Stylesheet) != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		view, err := compileMemoryView(p.Memory.Presentation.ViewSource, memoryViewBindings(p.Memory, r), requestLayout(r))
		if err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		fragment, err := renderMemoryView(view, p.Memory)
		if err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		configuration, err := memoryClientConfiguration(p.Memory)
		if err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		outer := requestLayout(r).Funcs(memoryPresentationFunctions(p.Memory)).Funcs(template.FuncMap{"memoryPageView": func() template.HTML { return fragment + configuration }})
		if _, err = outer.New("memory-page").Parse(`{{memoryPageView}}`); err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		var body bytes.Buffer
		if err := outer.Execute(&body, p); err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(body.Bytes())
		return
	}
	if p.Workbench != nil {
		outer, err := workbenchPageLayout(p, r)
		if err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		var body bytes.Buffer
		if err = outer.Execute(&body, p); err != nil {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write(body.Bytes())
		return
	}
	_ = requestLayout(r).Execute(w, p)
}
func conversationCursor(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("before")
	if raw == "" {
		return 0, nil
	}
	before, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || before <= 0 {
		return 0, strconv.ErrSyntax
	}
	return before, nil
}
func notice(code string) string {
	switch code {
	case "feedback-saved":
		return "Your response was saved. Ghost will review it in the conversation."
	case "schedule-saved":
		return "Schedule saved. Future triggers use these settings."
	case "language-saved":
		return "Language saved."
	case "added":
		return "Model saved. Verify the connection before using it."
	case "tested":
		return "Connection verified. You can now use this model for new work."
	case "active":
		return "Model selected for new work."
	case "saved":
		return "Notification preference saved."
	case "signed-out":
		return "Provider account signed out."
	}
	return ""
}
