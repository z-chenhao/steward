package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/legacyv1"
)

func (s *Server) addModel(r *http.Request, status Status) (llm.Profile, string, error) {
	if service := r.FormValue("service"); service != "" && service != "openai" && service != "custom" {
		return llm.Profile{}, "", llm.ErrConflict
	}
	facts := contract.ConnectionFacts{Service: r.FormValue("service"), WireAPI: r.FormValue("wire_api"), AccountBacked: r.FormValue("account") != "", Added: &contract.AddedFacts{HasCurrent: activeProfile(status) != ""}}
	defaults, err := s.connectionDefaults(r.Context(), facts)
	if err != nil {
		return llm.Profile{}, "", err
	}
	// Decide before persisting the candidate, from the same selected policy
	// result as its defaults. Invalid or stopped policy must not create a row.
	switch defaults.AddedAction {
	case "activate":
		if facts.Added.HasCurrent {
			return llm.Profile{}, "", llm.ErrConflict
		}
	case "probe", "save":
	default:
		return llm.Profile{}, "", llm.ErrConflict
	}
	input := llm.ProfileInput{Name: r.FormValue("model"), Endpoint: r.FormValue("endpoint"), ModelID: r.FormValue("model"), ProviderKind: "openai_compatible", WireAPI: defaults.WireAPI, ContextWindow: defaults.ContextWindow, MaxOutputTokens: defaults.MaxOutputTokens, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true, ImageInput: defaults.ImageInput}}
	if !facts.AccountBacked {
		// The submitted endpoint (or the explicit OpenAI service choice) is
		// credential destination authority. A replaceable policy cannot redirect
		// a newly supplied key to another origin or reinterpret that service.
		expected := input.Endpoint
		if facts.Service == "openai" {
			expected = "https://api.openai.com/v1"
		}
		if defaults.EndpointOverride != "" && defaults.EndpointOverride != expected || facts.Service == "openai" && defaults.EndpointOverride != expected {
			return llm.Profile{}, "", llm.ErrConflict
		}
	}
	if defaults.EndpointOverride != "" {
		input.Endpoint = defaults.EndpointOverride
	}
	if r.FormValue("account") != "" {
		input.AuthConnectionID = r.FormValue("account")
		input.ProviderKind = "openai_account"
		input.Endpoint = ""
		if s.AccountCatalog == nil {
			return llm.Profile{}, "", llm.ErrNoBridge
		}
		catalog, err := s.AccountCatalog(r.Context(), input.AuthConnectionID)
		if err != nil {
			return llm.Profile{}, "", err
		}
		found := false
		for _, m := range catalog {
			if m.ModelID == input.ModelID {
				input.ContextWindow = m.ContextWindow
				input.MaxOutputTokens = min(m.MaxOutputTokens, m.ContextWindow)
				input.Capabilities.ImageInput = m.ImageInput
				found = true
			}
		}
		if !found {
			return llm.Profile{}, "", llm.ModelIncompatible
		}
	}
	if name := r.FormValue("name"); name != "" {
		input.Name = name
	}
	if input.Validate() != nil {
		return llm.Profile{}, "", llm.ModelIncompatible
	}
	key := []byte(strings.TrimSpace(r.FormValue("api_key")))
	defer func() { r.Form.Del("api_key"); r.PostForm.Del("api_key") }()
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	if r.FormValue("service") == "openai" && len(key) == 0 {
		return llm.Profile{}, "", llm.AuthRequired
	}
	if len(key) > 0 {
		writer, ok := s.Models.(interface {
			AddKeyProfile(context.Context, llm.ProfileInput, llm.SecretStore, []byte) (llm.Profile, error)
		})
		if !ok {
			return llm.Profile{}, "", llm.ErrNoBridge
		}
		profile, err := writer.AddKeyProfile(r.Context(), input, s.Secrets, key)
		return profile, defaults.AddedAction, err
	}
	profile, err := s.Models.AddProfile(r.Context(), input)
	return profile, defaults.AddedAction, err
}
func (s *Server) testModel(r *http.Request) error {
	ctx := r.Context()
	id := r.FormValue("profile")
	if r.FormValue("restore_current") != "yes" {
		_, err := s.Control.Test(ctx, id)
		return err
	}

	// Testing the current model can restore its existing selection after an
	// auth loss or stale proof. A concurrent switch must win over this restore.
	status, err := s.Core.WebStatus(ctx)
	if err != nil {
		return err
	}
	if activeProfile(status) != id || strconv.FormatInt(status.ConfigVersion, 10) != r.FormValue("config_version") {
		return llm.ErrConflict
	}
	return s.activateModel(r)
}

func (s *Server) selectModel(r *http.Request) error {
	if r.FormValue("consent") != "yes" {
		return llm.ErrConflict
	}
	return s.activateModel(r)
}

func (s *Server) activateModel(r *http.Request) error {
	pv, e := strconv.ParseInt(r.FormValue("profile_version"), 10, 64)
	if e != nil {
		return e
	}
	cv, e := strconv.ParseInt(r.FormValue("config_version"), 10, 64)
	if e != nil {
		return e
	}
	if e = s.Control.SelectWithProbe(r.Context(), r.FormValue("profile"), pv, cv); e != nil {
		return e
	}
	if e = s.Core.WaitForRuntime(r.Context()); e != nil {
		return e
	}

	return nil
}

func (s *Server) disconnectKeyModel(w http.ResponseWriter, r *http.Request) error {
	if r.FormValue("confirm") != "yes" {
		return llm.ErrConflict
	}
	version, e := strconv.ParseInt(r.FormValue("profile_version"), 10, 64)
	if e != nil {
		return llm.ErrConflict
	}
	authVersion, e := strconv.ParseInt(r.FormValue("auth_version"), 10, 64)
	if e != nil {
		return llm.ErrConflict
	}
	writer, ok := s.Models.(interface {
		DisconnectKeyProfile(context.Context, string, int64, int64, llm.SecretStore) error
	})
	if !ok {
		return llm.ErrNoBridge
	}
	if err := writer.DisconnectKeyProfile(r.Context(), r.FormValue("profile"), version, authVersion, s.Secrets); err != nil {
		return err
	}
	http.Redirect(w, r, "/settings/models", http.StatusSeeOther)
	return nil
}

func (s *Server) reconnectModel(w http.ResponseWriter, r *http.Request) error {
	version, err := strconv.ParseInt(r.FormValue("profile_version"), 10, 64)
	if err != nil {
		return llm.ErrConflict
	}
	profile, err := s.Control.Store.Profile(r.Context(), r.FormValue("profile"))
	if err != nil || profile.Version != version {
		return llm.ErrConflict
	}
	status, err := s.Core.WebStatus(r.Context())
	if err != nil {
		return err
	}
	current := activeProfile(status)
	policy, err := s.connectionDefaults(r.Context(), contract.ConnectionFacts{Reconnect: &contract.ReconnectFacts{HasCurrent: current != "", TargetCurrent: current == profile.ProfileID}})
	if err != nil {
		return err
	}
	switch policy.ReconnectAction {
	case "activate":
		// Reconnect consent cannot replace another current selection. The actual
		// activation still uses the submitted profile/configuration version fences.
		if current != "" && current != profile.ProfileID {
			return llm.ErrConflict
		}
		err = s.activateModel(r)
	case "probe":
		_, err = s.Control.Test(r.Context(), profile.ProfileID)
	default:
		return llm.ErrConflict
	}
	if err != nil {
		return err
	}
	http.Redirect(w, r, "/settings/models", http.StatusSeeOther)
	return nil
}

func (s *Server) connectionDefaults(ctx context.Context, facts contract.ConnectionFacts) (contract.ConnectionDefaults, error) {
	if s.ModelConnectionPolicy != nil {
		out, managed, err := s.ModelConnectionPolicy(ctx, facts)
		if err != nil || managed {
			return out, err
		}
	}
	return legacyv1.Connection(facts), nil
}
