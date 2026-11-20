package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/interaction/feishu"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
)

func TestServiceInspectorDeliversOneConsentedFaultAndRecoveryAfterRestart(t *testing.T) {
	for _, loseReceipt := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost-reply-%t", loseReceipt), func(t *testing.T) {
			ctx := context.Background()
			m, _ := fixture(t)
			now := time.Now().UTC()
			m.now = func() time.Time { return now }
			type message struct {
				Chat    string `json:"receive_id"`
				Key     string `json:"uuid"`
				Kind    string `json:"msg_type"`
				Content string `json:"content"`
			}
			var lock sync.Mutex
			var messages []message
			const token = "private-token-for-alert-fixture"
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/open-apis/im/v1/messages" || r.URL.Query().Get("receive_id_type") != "chat_id" || r.Header.Get("Authorization") != "Bearer "+token {
					t.Errorf("unexpected alert request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(400)
					return
				}
				var msg message
				if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
					t.Error(err)
					return
				}
				lock.Lock()
				messages = append(messages, msg)
				n := len(messages)
				lock.Unlock()
				if n == 1 {
					other, err := New(m.root, m.home, m.executable)
					if err != nil {
						t.Error(err)
						return
					}
					// A new inspector can read local status while the network send is
					// outstanding. It sees precommitted uncertainty and cannot resend.
					pending, err := other.Status()
					if err != nil || pending.Alerts == nil || pending.Alerts.Fault == nil || pending.Alerts.Fault.Outcome != "unknown" {
						t.Errorf("durable admission before response: %v %v", pending, err)
					}
					if err := other.dispatchAlert(ctx, nil); !errors.Is(err, ErrBusy) {
						t.Errorf("concurrent inspector admission: %v", err)
					}
				}
				if loseReceipt && n == 1 {
					c, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					c.Close()
					return
				}
				fmt.Fprint(w, `{"code":0,"data":{"message_id":"om_test"}}`)
			}))
			defer peer.Close()
			count := func() int { lock.Lock(); defer lock.Unlock(); return len(messages) }
			cfg := feishu.Config{AppID: "fixture", TenantKey: "fixture", VerificationTokenRef: "credential:verify", EncryptKeyRef: "credential:encrypt", AccessTokenRef: "credential:alert-access", APIBase: peer.URL}
			secrets := secretmemory.New()
			must(t, secrets.Put(ctx, cfg.AccessTokenRef, []byte(token)))
			must(t, m.Install(ctx, ""))
			must(t, m.Start(ctx))
			session, _, err := m.Begin()
			must(t, err)
			must(t, session.FreezeRestartRecipe(&RestartReceipt{GraphRevision: "sha256:" + strings.Repeat("c", 64), Recipe: RestartRecipe{Attempts: 3, RetryDelayMS: 60000, AlertAfterSeconds: 60}}))
			must(t, session.Heartbeat("dependency_unavailable"))
			inspect := func(m *Manager) Status { t.Helper(); v, e := m.InspectAndNotify(ctx, secrets); must(t, e); return v }
			inspect(m)
			now = now.Add(2 * time.Minute)
			inspect(m)
			if count() != 0 {
				t.Fatal("unsolicited alert")
			}
			if m.ConfigureFeishuAlerts("../not-a-chat", cfg) == nil {
				t.Fatal("invalid route")
			}
			must(t, m.ConfigureFeishuAlerts("oc_selected", cfg))
			must(t, session.Heartbeat("dependency_unavailable"))
			inspect(m)
			now = now.Add(59 * time.Second)
			must(t, session.Close())
			inspect(m)
			if count() != 0 {
				t.Fatal("not a sustained fault")
			}
			// New inspector objects share only OS locks, durable settings and the secret store.
			reopened, err := New(m.root, m.home, m.executable)
			must(t, err)
			reopened.now = m.now
			reopened.command = m.command
			now = now.Add(time.Second)
			failed := inspect(reopened)
			if failed.Running || failed.Alerts == nil || failed.Alerts.Fault == nil || count() != 1 {
				t.Fatal("fault delivery", failed, count())
			}
			outcome := "delivered"
			if loseReceipt {
				outcome = "unknown"
			}
			if failed.Alerts.Fault.Outcome != outcome || failed.Alerts.Fault.Code != "process_exited" {
				t.Fatal("incorrect outage receipt", failed.Alerts.Fault)
			}
			must(t, reopened.ConfigureFeishuAlerts("oc_selected", cfg))
			now = now.Add(5 * time.Minute)
			inspect(m)
			inspect(reopened)
			if count() != 1 {
				t.Fatal("replayed the same outage")
			}
			resumed, _, err := reopened.Begin()
			must(t, err)
			must(t, resumed.Heartbeat("ready"))
			recovered := inspect(m)
			if count() != 2 || recovered.Alerts == nil || recovered.Alerts.Recovery == nil || recovered.Alerts.Recovery.Outcome != "delivered" || recovered.Alerts.Fault.Outcome != outcome {
				t.Fatal("missing recovery history", recovered.Alerts, count())
			}
			inspect(reopened)
			if count() != 2 {
				t.Fatal("replayed recovery")
			}
			lock.Lock()
			fault, recovery := messages[0], messages[1]
			lock.Unlock()
			if fault.Chat != "oc_selected" || recovery.Chat != fault.Chat || fault.Kind != "text" || recovery.Kind != "text" || len(fault.Key) != 32 || fault.Key == recovery.Key || !strings.Contains(fault.Content, "process_exited") || !strings.Contains(recovery.Content, "ready") {
				t.Fatal(fault, recovery)
			}
			raw, err := os.ReadFile(m.alertPath())
			must(t, err)
			if strings.Contains(string(raw), token) || strings.Contains(string(raw), "credential:verify") || strings.Contains(string(raw), "credential:encrypt") {
				t.Fatal("private material beyond the send route was persisted")
			}
			statusJSON, _ := json.Marshal(recovered)
			if strings.Contains(string(statusJSON), "oc_selected") || strings.Contains(string(statusJSON), "credential:") {
				t.Fatal("private route appeared in operator diagnostics")
			}
			must(t, reopened.DisableAlerts())
			must(t, resumed.Close())
			inspect(m)
			now = now.Add(2 * time.Minute)
			inspect(reopened)
			if count() != 2 {
				t.Fatal("messages after opt out")
			}
		})
	}
}
func TestServiceInspectorOldRecipeKeepsAlertsLocal(t *testing.T) {
	ctx := context.Background()
	m, _ := fixture(t)
	now := time.Now()
	m.now = func() time.Time { return now }
	cfg := feishu.Config{AppID: "fixture", TenantKey: "fixture", VerificationTokenRef: "credential:verify", EncryptKeyRef: "credential:encrypt", AccessTokenRef: "credential:access"}
	must(t, m.Install(ctx, ""))
	must(t, m.Start(ctx))
	session, _, err := m.Begin()
	must(t, err)
	must(t, session.FreezeRestartRecipe(&RestartReceipt{GraphRevision: "sha256:" + strings.Repeat("c", 64), Recipe: RestartRecipe{Attempts: 0, RetryDelayMS: 60000}}))
	must(t, session.Close())
	must(t, m.ConfigureFeishuAlerts("oc_selected", cfg))
	_, err = m.InspectAndNotify(ctx, nil)
	must(t, err)
	now = now.Add(5 * time.Minute)
	v, err := m.InspectAndNotify(ctx, nil)
	must(t, err)
	if v.Code != "restart_limit" || v.Alerts == nil || v.Alerts.Policy != "waiting_for_watchdog_recipe" || v.Alerts.Fault != nil {
		t.Fatal(v)
	}
}

func TestServiceInspectorCorruptAlertFilePreservesLocalHealth(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	must(t, m.Install(ctx, ""))
	must(t, os.WriteFile(m.alertPath(), []byte("damaged-private-data"), 0600))
	v, err := m.Status()
	must(t, err)
	if v.Code != "stopped" || v.Alerts == nil || v.Alerts.Policy != "unavailable" {
		t.Fatal(v)
	}
	v, err = m.InspectAndNotify(ctx, nil)
	if err == nil || v.Code != "stopped" || v.Alerts == nil || v.Alerts.Policy != "unavailable" || strings.Contains(err.Error(), "damaged-private-data") {
		t.Fatal(v, err)
	}
}
