package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Embedded production App tests use their own executable as the sandbox
// launcher, just as the distributed Steward CLI does.
func TestMain(m *testing.M) {
	if handled, err := rsi.ExecPlugin(os.Args[1:]); handled {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestMeetingPreparationDSTWindow(t *testing.T) {
	for _, tc := range []struct {
		now   string
		hours time.Duration
	}{{"2026-03-07T12:00:00Z", 23}, {"2026-10-31T12:00:00Z", 25}} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		start, end, err := tomorrowWindow(now, "America/New_York")
		if err != nil || end.Sub(start) != tc.hours*time.Hour || start.Hour() != 0 || end.Hour() != 0 {
			t.Fatal(start, end, err)
		}
	}
}
func TestMeetingPreparationProductionWebAndRevocation(t *testing.T) {
	carrier := os.Getenv("STEWARD_TEST_APP_DEFAULT_BUNDLE")
	if carrier == "" {
		t.Skip("requires production package inventory")
	}
	carrier, err := filepath.Abs(carrier)
	if err != nil {
		t.Fatal(err)
	}
	d, err := builtin.ShippedDirectories(carrier)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := secretmemory.New()
	if err = secrets.Put(ctx, "credential:prep-test", []byte("owned-project-key")); err != nil {
		t.Fatal(err)
	}
	a, err := open(ctx, cfg, "", secrets, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	a.prepareFeaturePlugins = func(ctx context.Context) error {
		w, e := prepareWebDefault(ctx, host, cfg, d.Extract)
		if e != nil {
			return e
		}
		return builtin.EnsureDefault(ctx, host, d, w)
	}
	if err = a.prepareFeaturePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := a.connectedApps.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.UserID, st.KeyRef = "owned-user", "credential:prep-test"
	st.Connections = []connectedapps.Connection{
		{ID: "cal", Name: "Calendar", Alias: "Work", Status: "active", Toolkit: "googlecalendar", AccountID: "ca_cal"},
		{ID: "mail", Name: "Mail", Alias: "Work", Status: "active", Toolkit: "gmail", AccountID: "ca_mail"},
		{ID: "other", Name: "Mail", Alias: "Other", Status: "active", Toolkit: "gmail", AccountID: "ca_other"},
	}
	if err = a.store.SaveApps(ctx, st, st.Revision); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	paths := []string{}
	mailSubject := "Re: Release review"
	remoteRevoked, revokeDuringRead := false, false
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("x-api-key") != "owned-project-key" {
			t.Error("missing project credential")
		}
		if strings.HasPrefix(r.URL.Path, "/connected_accounts/") {
			id := strings.TrimPrefix(r.URL.Path, "/connected_accounts/")
			tk := "googlecalendar"
			status := "ACTIVE"
			if id == "ca_mail" {
				tk = "gmail"
				if remoteRevoked {
					status = "REVOKED"
				}
			} else if id != "ca_cal" {
				t.Errorf("unexpected account: %s", id)
			}
			json.NewEncoder(w).Encode(map[string]any{"id": id, "user_id": "owned-user", "status": status, "toolkit": map[string]any{"slug": tk}})
			return
		}
		var b struct {
			Endpoint, Method string
			Account          string `json:"connected_account_id"`
			User             string `json:"user_id"`
		}
		if r.URL.Path != "/tools/execute/proxy" || r.Method != "POST" || json.NewDecoder(r.Body).Decode(&b) != nil || b.Method != "GET" || b.User != "owned-user" {
			t.Error("unexpected effect or proxy envelope")
			w.WriteHeader(400)
			return
		}
		u, e := url.Parse(b.Endpoint)
		if e != nil {
			t.Error(e)
			return
		}
		paths = append(paths, u.Path)
		var data any
		switch u.Path {
		case "/calendar/v3/calendars/primary/events":
			start, e := time.Parse(time.RFC3339, u.Query().Get("timeMin"))
			if e != nil || u.Host != "www.googleapis.com" || b.Account != "ca_cal" || u.Query().Get("maxResults") != "20" {
				t.Error("wrong calendar scope", b.Endpoint)
			}
			data = map[string]any{"items": []any{map[string]any{"id": "event", "summary": "Release review", "htmlLink": "https://calendar.google.com/calendar/event?eid=owned", "start": map[string]any{"dateTime": start.Add(10 * time.Hour).Format(time.RFC3339)}}}}
			if revokeDuringRead {
				remoteRevoked = true
			}
		case "/gmail/v1/users/me/threads":
			if b.Account != "ca_mail" || u.Host != "gmail.googleapis.com" || u.Query().Get("maxResults") != "10" {
				t.Error("wrong thread scope")
			}
			data = map[string]any{"threads": []any{map[string]string{"id": "abc123"}}}
		case "/gmail/v1/users/me/threads/abc123":
			if b.Account != "ca_mail" || u.Query().Get("format") != "metadata" || u.Query().Get("metadataHeaders") != "Subject" {
				t.Error("read beyond subject")
			}
			data = map[string]any{"id": "abc123", "messages": []any{map[string]any{"payload": map[string]any{"headers": []any{map[string]any{"name": "Subject", "value": mailSubject}}}}}}
		case "/gmail/v1/users/me/profile":
			if b.Account != "ca_mail" || u.Query().Get("fields") != "emailAddress" {
				t.Error("wrong account identity")
			}
			data = map[string]any{"emailAddress": "owner@example.test"}
		default:
			t.Errorf("unexpected endpoint %s", u.Path)
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data, "status": 200})
	}))
	defer peer.Close()
	a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
	if _, err = a.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	webServer := &web.Server{Core: a, Models: a.Models(), Apps: a.connectedApps, Secrets: secrets}
	server := httptest.NewServer(webServer)
	defer server.Close()
	browserEvidence := os.Getenv("STEWARD_MEETING_BROWSER_EVIDENCE")
	if browserEvidence == "" {
		browserEvidence = t.TempDir()
	}
	driver := exec.Command("node", "../../guest-browser/meeting-preparation.mjs", server.URL, browserEvidence)
	if _, e := os.Stat("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"); e == nil {
		driver.Env = append(os.Environ(), "STEWARD_BROWSER_EXECUTABLE=/Applications/Google Chrome.app/Contents/MacOS/Google Chrome")
	}
	if out, e := driver.CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	current, _ := a.connectedApps.State(ctx)
	if current.Meeting != nil {
		t.Fatal("web revoke did not clear the grant")
	}
	mu.Lock()
	count := len(paths)
	mu.Unlock()
	a.refreshMeeting(ctx, time.Now().Add(24*time.Hour))
	mu.Lock()
	if len(paths) != count || count != 4 {
		t.Errorf("disable read content: %v", paths)
	}
	revokeDuringRead = true
	mu.Unlock()
	if err = a.ConfigureMeeting(ctx, current.Revision, "cal", "mail", "UTC", false); err != nil {
		t.Fatal(err)
	}
	view, err := a.MeetingPreparation(ctx)
	if err != nil || view.Status != "revoked" || view.Brief.Subject != "" {
		t.Fatal("revoked result escaped", view, err)
	}
	mu.Lock()
	if len(paths) != count+1 || paths[len(paths)-1] != "/calendar/v3/calendars/primary/events" {
		t.Errorf("mail content read after revocation: %v", paths)
	}
	revokeDuringRead = false
	remoteRevoked = false
	mu.Unlock()
	current, _ = a.connectedApps.State(ctx)
	if err = a.ConfigureMeeting(ctx, current.Revision, "cal", "mail", "UTC", false); err != nil {
		t.Fatal(err)
	}
	view, _ = a.MeetingPreparation(ctx)
	if view.Status != "ready" {
		t.Fatal("new explicit grant could not prepare", view)
	}
	// The same authorized accounts and grant now exercise a genuinely different
	// policy. Coldly selected managed code changes the answer; rollback restores it.
	mu.Lock()
	mailSubject = "AW: Release review"
	mu.Unlock()
	current, _ = a.connectedApps.State(ctx)
	if err = a.ConfigureMeeting(ctx, current.Revision, "cal", "mail", "UTC", false); err != nil {
		t.Fatal(err)
	}
	view, _ = a.MeetingPreparation(ctx)
	if view.Status != "empty" {
		t.Fatal("original version unexpectedly accepts AW", view)
	}
	initial, _ := host.Store.Status(ctx)
	baseline, err := host.Store.Graph(ctx, initial.Revision)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := host.Install(ctx, meetingCandidatePackage(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := range baseline.Graph.Bindings {
		if baseline.Graph.Bindings[i].InstanceID == "meeting-preparation" {
			baseline.Graph.Bindings[i].PackageDigest = pkg.Digest
		}
	}
	target, err := host.Prepare(ctx, baseline.Graph)
	if err != nil {
		t.Fatal(err)
	}
	var owner string
	if err = a.store.Transaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT policy_id FROM rsi_candidate WHERE target=?", initial.Revision).Scan(&owner)
	}); err != nil {
		t.Fatal(err)
	}
	activate := func(id, base, next string) {
		t.Helper()
		e := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: base, Target: next, PolicyID: owner, Goal: "Check meeting subject policy"})
		if e != nil {
			t.Fatal(e)
		}
		decision, e := host.Verify(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = host.Activate(ctx, rsi.Activation{OperationID: id, Expected: base, Target: next, DecisionID: decision}); e != nil {
			t.Fatal(e)
		}
	}
	activate("meeting-upgrade", initial.Revision, target.Revision)
	a.refreshMeeting(ctx, time.Now())
	view, _ = a.MeetingPreparation(ctx)
	if view.Status != "ready" || view.Brief.Subject != "AW: Release review" {
		t.Fatal("upgraded selection failed", view)
	}
	activate("meeting-rollback", target.Revision, initial.Revision)
	a.refreshMeeting(ctx, time.Now())
	view, _ = a.MeetingPreparation(ctx)
	if view.Status != "empty" {
		t.Fatal("rollback retained stale recommendation", view)
	}
	mu.Lock()
	mailSubject = "Re: Release review"
	mu.Unlock()
	current, _ = a.connectedApps.State(ctx)
	if err = a.ConfigureMeeting(ctx, current.Revision, "cal", "mail", "UTC", false); err != nil {
		t.Fatal(err)
	}

	status, _ := host.Store.Status(ctx)
	if _, err = host.Store.Deactivate(ctx, "stop-meeting-preparation", status.Revision); err != nil {
		t.Fatal(err)
	}
	view, _ = a.MeetingPreparation(ctx)
	if view.Brief.Subject != "" || view.Status != "unavailable" {
		t.Fatal("stopped owner released private result", view)
	}
}

func meetingCandidatePackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src, err := filepath.Abs("../plugin/meetingprep/policy.go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.Replace(string(raw), `Version: "1.0.0"`, `Version: "1.1.0"`, 1)
	code = strings.Replace(code, `prefix != "fwd" {`, `prefix != "fwd" && prefix != "aw" {`, 1)
	patch := filepath.Join(t.TempDir(), "policy.go")
	if err = os.WriteFile(patch, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{src: patch}})
	of := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(of, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if out, e := exec.Command("go", "build", "-overlay", of, "-o", filepath.Join(dir, "plugin"), "../../cmd/plugins/meeting-preparation").CombinedOutput(); e != nil {
		t.Fatal(string(out), e)
	}
	raw, err = os.ReadFile("../../plugins/meeting-preparation/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.1.0"
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
