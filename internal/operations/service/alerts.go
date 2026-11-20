package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/interaction/feishu"
	"github.com/z-chenhao/assistant/internal/llm"
)

// Only fixed status codes and this one consented Feishu chat leave the inspector.
// Credential values stay in SecretStore. No runtime/database access is required.
type alertBinding struct {
	ID     string        `json:"id"`
	Chat   string        `json:"chat"`
	Config feishu.Config `json:"config"`
}
type AlertReceipt struct {
	Code    string    `json:"code"`
	Outcome string    `json:"outcome"`
	At      time.Time `json:"at"`
}
type AlertStatus struct {
	Enabled  bool          `json:"enabled,omitempty"`
	Policy   string        `json:"policy"`
	Fault    *AlertReceipt `json:"last_fault,omitempty"`
	Recovery *AlertReceipt `json:"last_recovery,omitempty"`
}
type alertState struct {
	Binding    *alertBinding `json:"binding,omitempty"`
	Deployment int64         `json:"deployment,omitempty"`
	Started    int64         `json:"fault_since,omitempty"`
	Admitted   bool          `json:"admitted,omitempty"`
	Fault      *AlertReceipt `json:"last_fault,omitempty"`
	Recovery   *AlertReceipt `json:"last_recovery,omitempty"`
}

func (m *Manager) alertPath() string { return filepath.Join(m.dir(), "alerts.json") }
func (m *Manager) loadAlerts() (alertState, error) {
	var state alertState
	err := readJSON(m.alertPath(), &state)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err == nil && state.Binding != nil {
		b := state.Binding
		if !validAlertChat(b.Chat) || len(b.ID) != 32 || strings.Trim(b.ID, "0123456789abcdef") != "" || b.Config.ValidateOutgoing() != nil {
			return alertState{}, errors.New("invalid configured alert route")
		}
	}
	return state, err
}
func (m *Manager) alertStatus(s settings) (*AlertStatus, error) {
	a, err := m.loadAlerts()
	if err != nil {
		return nil, err
	}
	if a.Binding == nil && a.Fault == nil && a.Recovery == nil {
		return nil, nil
	}
	status := &AlertStatus{Enabled: a.Binding != nil, Policy: "disabled", Fault: a.Fault, Recovery: a.Recovery}
	if status.Enabled {
		status.Policy = "waiting_for_watchdog_recipe"
		if recipe, ok := s.restartRecipe(); ok && recipe.AlertAfterSeconds > 0 {
			status.Policy = "active"
		}
	}
	return status, nil
}
func (m *Manager) DeploymentConfigPath() (string, error) {
	s, err := m.load()
	return s.ConfigPath, err
}
func validAlertChat(id string) bool {
	return len(id) >= 4 && len(id) <= 128 && strings.HasPrefix(id, "oc_") && strings.Trim(id[3:], "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_-") == ""
}

func (m *Manager) ConfigureFeishuAlerts(chat string, cfg feishu.Config) error {
	if cfg.Validate() != nil || !validAlertChat(chat) {
		return errors.New("a configured Feishu account and exact oc_ chat ID are required")
	}
	// Incoming webhook and workspace fields do not authorize outgoing diagnostics.
	cfg = feishu.Config{AppID: cfg.AppID, TenantKey: cfg.TenantKey, APIBase: cfg.APIBase, AccessTokenRef: cfg.AccessTokenRef, AppSecretRef: cfg.AppSecretRef}
	lock, err := m.lock("alerts.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	state, err := m.loadAlerts()
	if err != nil {
		return err
	}
	if state.Binding != nil && state.Binding.Chat == chat && state.Binding.Config == cfg {
		return nil
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return err
	}
	return writeJSON(m.alertPath(), alertState{Binding: &alertBinding{ID: hex.EncodeToString(id), Chat: chat, Config: cfg}})
}
func (m *Manager) DisableAlerts() error {
	lock, err := m.lock("alerts.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	state, err := m.loadAlerts()
	if err != nil || state.Binding == nil {
		return err
	}
	state.Binding = nil
	state.Started = 0
	state.Admitted = false
	return writeJSON(m.alertPath(), state)
}

// InspectAndNotify never holds the service control lock across a network call.
// The independent alert lock serializes consent changes and competing inspectors.
func (m *Manager) InspectAndNotify(ctx context.Context, secrets llm.SecretStore) (Status, error) {
	v, checkErr := m.Inspect(ctx)
	if errors.Is(checkErr, ErrBusy) {
		return v, checkErr
	}
	if err := m.dispatchAlert(ctx, secrets); err != nil {
		v.Alerts = &AlertStatus{Policy: "unavailable"}
		return v, errors.Join(checkErr, err)
	}
	current, err := m.Status()
	if err != nil {
		return v, errors.Join(checkErr, err)
	}
	v.Alerts = current.Alerts
	return v, checkErr
}

func (m *Manager) dispatchAlert(ctx context.Context, secrets llm.SecretStore) error {
	lock, err := m.lock("alerts.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	state, err := m.loadAlerts()
	if err != nil || state.Binding == nil {
		return err
	}
	plan, err := m.admitAlert(&state)
	if err != nil || plan == nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := feishu.NewClient(state.Binding.Config, secrets)
	defer client.Close()
	_, err = client.Send(ctx, state.Binding.Chat, plan.key, "text", map[string]string{"text": plan.text})
	switch {
	case err == nil:
		plan.receipt.Outcome = "delivered"
	case errors.Is(err, feishu.ErrAuthentication) || errors.Is(err, feishu.ErrCredentialsRejected) || errors.Is(err, feishu.ErrCredentialsUnavailable):
		plan.receipt.Outcome = "credential_unavailable"
	}
	return writeJSON(m.alertPath(), state)
}

type alertPlan struct {
	key, text string
	receipt   *AlertReceipt
}

// Hold the control lock from the enabled/health check through durable admission.
// Transport runs afterwards, independently of restart and stop operations.
func (m *Manager) admitAlert(state *alertState) (*alertPlan, error) {
	control, err := m.lock("control.lock")
	if err != nil {
		return nil, err
	}
	defer control.Close()
	s, err := m.load()
	var latest Status
	if err == nil {
		latest, err = m.status(s)
	}
	if err != nil {
		return nil, err
	}
	recipe, ok := s.restartRecipe()
	if !s.Enabled || !s.Installed || !ok || recipe.AlertAfterSeconds == 0 {
		return nil, nil
	}
	if state.Deployment != s.Deployment && !state.Admitted {
		state.Started = 0
	}
	state.Deployment = s.Deployment
	now := m.now().UTC()
	code := latest.Code
	recovery := code == "ready"
	switch {
	case recovery && !state.Admitted:
		if state.Started == 0 {
			return nil, nil
		}
		state.Started = 0
		return nil, writeJSON(m.alertPath(), state)
	case !recovery:
		if code == "starting" || state.Admitted {
			return nil, nil
		}
		if state.Started == 0 || now.UnixMicro() < state.Started {
			state.Started = now.UnixMicro()
			return nil, writeJSON(m.alertPath(), state)
		}
		if now.Sub(time.UnixMicro(state.Started)) < time.Duration(recipe.AlertAfterSeconds)*time.Second {
			return nil, nil
		}
	}
	kind := "fault"
	receipt := &AlertReceipt{Code: code, Outcome: "unknown", At: now}
	if recovery {
		kind = "recovery"
		state.Recovery = receipt
	} else {
		state.Fault = receipt
	}
	sum := sha256.Sum256([]byte(state.Binding.ID + "|" + time.UnixMicro(state.Started).UTC().Format(time.RFC3339Nano) + "|" + kind))
	state.Admitted = !recovery
	if recovery {
		state.Started = 0
	}
	// A crash anywhere after admission is an unknown external effect. No replay.
	if err := writeJSON(m.alertPath(), state); err != nil {
		return nil, err
	}
	text := "Steward 服务异常（" + code + "）。请在本机查看 steward service status。"
	if recovery {
		text = "Steward 服务已恢复（ready）。"
	}
	return &alertPlan{key: hex.EncodeToString(sum[:16]), text: text, receipt: receipt}, nil
}
