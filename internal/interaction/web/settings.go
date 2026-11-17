package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	modelsettings "github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"unicode/utf8"
)

type settingsView struct {
	Copy                  *modelsettings.View
	View                  *modelsettings.View
	TargetUnavailable     bool
	Section               string
	TargetModel           string
	DirectModels          []modelResource
	AccountModels         map[string][]modelResource
	Target                string
	Model, Endpoint, Name string
	Service, WireAPI      string
	ConnectionMethod      string
}

type modelResource struct {
	llm.Profile
	Current, Verified, Open bool
	KeyAuth                 bool
	AuthVersion             int64
	ConnectionStatus        string
	ConfigVersion           int64
}

func resourceModelQuery(id, notice string) url.Values {
	query := url.Values{"model": {id}}
	if notice != "" {
		query.Set("notice", notice)
	}
	return query
}

func resourceModelURL(id, notice string) string {
	return "/settings/models?" + resourceModelQuery(id, notice).Encode() + "#model-" + url.PathEscape(id)
}

func (s *Server) loadSettings(ctx context.Context, r *http.Request, p *page) error {
	v := &settingsView{Section: "models"}
	p.Settings = v
	if r.URL.Path == "/settings/preferences" {
		v.Section = "preferences"
		return nil
	}
	if r.URL.Path == "/settings/notifications" {
		v.Section = "notifications"
		var err error
		p.Bindings, err = s.Core.ChannelBindings(ctx)
		if err != nil {
			return err
		}
		p.Preference, err = s.Core.ChannelPreference(ctx)
		if err != nil {
			return err
		}
		v.Target = p.Preference.Channel + ":"
		if p.Preference.BindingID != nil {
			v.Target += *p.Preference.BindingID
		}
		if p.Preference.Channel == "" {
			v.Target = "auto:"
		}
		if v.Target != "auto:" && v.Target != "tui:" {
			v.TargetUnavailable = true
			for _, b := range p.Bindings {
				if b.Enabled && bindingTarget(b) == v.Target {
					v.TargetUnavailable = false
				}
			}
		}
		return nil
	}
	if r.URL.Path == "/settings/models/new" {
		v.Section = "new"
		var sourceProfile *llm.Profile
		facts := modelsettings.SettingsFacts{Rows: []modelsettings.SettingsRow{}, WithView: true, Draft: &modelsettings.DraftFacts{Method: r.URL.Query().Get("method")}}
		if source := r.URL.Query().Get("source"); source != "" {
			if s.Control.Store == nil {
				return errPageNotFound
			}
			profile, err := s.Control.Store.Profile(ctx, source)
			if err != nil || profile.ProviderKind != "openai_compatible" {
				return errPageNotFound
			}
			sourceProfile = &profile
			facts.Draft.HasSource = true
			facts.Draft.OpenAIEndpoint = profile.Endpoint == "https://api.openai.com/v1"
		}
		projected, err := s.projectModelSettings(ctx, facts)
		if err != nil {
			return err
		}
		v.View, v.Copy = projected.View, projected.Copy
		draft := projected.Draft
		if draft == nil || len(projected.Rows) != 0 || draft.UseSource && sourceProfile == nil {
			return llm.ErrConflict
		}
		for _, value := range []string{draft.Method, draft.Endpoint, draft.Service, draft.WireAPI} {
			if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
				return llm.ErrConflict
			}
		}
		v.ConnectionMethod, v.Endpoint, v.Service, v.WireAPI = draft.Method, draft.Endpoint, draft.Service, draft.WireAPI
		if draft.UseSource {
			v.Model, v.Endpoint, v.Name, v.WireAPI = sourceProfile.ModelID, sourceProfile.Endpoint, sourceProfile.Name, sourceProfile.WireAPI
		}
		return nil
	}
	if s.Accounts != nil {
		var err error
		p.Accounts, err = s.Accounts.Store.Accounts(ctx)
		if err != nil {
			return err
		}
	}
	return s.loadResourceModels(ctx, r, p)
}

func (s *Server) loadResourceModels(ctx context.Context, r *http.Request, p *page) error {
	profiles, err := s.Models.ListProfiles(ctx)
	if err != nil {
		return err
	}
	v := p.Settings
	v.AccountModels = make(map[string][]modelResource, len(p.Accounts))
	for _, account := range p.Accounts {
		v.AccountModels[account.ConnectionID] = nil
	}
	requested := r.URL.Query().Get("model")
	notice := r.URL.Query().Get("notice")
	if strings.HasPrefix(r.URL.Path, "/settings/models/") {
		requested = strings.TrimPrefix(r.URL.Path, "/settings/models/")
	}
	facts := modelsettings.SettingsFacts{Rows: []modelsettings.SettingsRow{}, WithView: true, Requested: requested, Notice: notice}
	sources := map[string]modelResource{}
	for _, profile := range profiles {
		row := modelResource{Profile: profile, Current: profile.ProfileID == activeProfile(p.Status), Verified: s.profileVerified(ctx, profile), ConfigVersion: p.Status.ConfigVersion, ConnectionStatus: "connected"}
		if profile.AuthConnectionID != "" && profile.ProviderKind != "openai_account" && s.Control.Store != nil {
			auth, e := s.Control.Store.Auth(ctx, profile.AuthConnectionID)
			if e == nil {
				row.KeyAuth = auth.AuthKind == "api_key"
				row.AuthVersion = auth.Version
				row.ConnectionStatus = auth.Status
			}
		}
		_, known := v.AccountModels[profile.AuthConnectionID]
		facts.Rows = append(facts.Rows, modelsettings.SettingsRow{Ref: profile.ProfileID, Account: profile.AuthConnectionID, KnownAccount: known, Verified: row.Verified, Current: row.Current, AuthStatus: row.ConnectionStatus})
		sources[profile.ProfileID] = row
		if profile.ProfileID == requested {
			v.TargetModel = profile.ProfileID
		}
	}
	if requested != "" && v.TargetModel == "" {
		return errPageNotFound
	}
	projected, err := s.projectModelSettings(ctx, facts)
	if err != nil {
		return err
	}
	v.View, v.Copy = projected.View, projected.Copy
	if projected.Draft != nil {
		return llm.ErrConflict
	}
	if len(projected.Rows) != len(facts.Rows) {
		return llm.ErrConflict
	}
	seen := map[string]bool{}
	for _, choice := range projected.Rows {
		row, ok := sources[choice.Ref]
		if !ok || seen[choice.Ref] || !utf8.ValidString(choice.Status) || strings.ContainsRune(choice.Status, 0) {
			return llm.ErrConflict
		}
		seen[choice.Ref] = true
		if choice.Account != "" {
			if _, known := v.AccountModels[choice.Account]; !known || choice.Account != row.AuthConnectionID {
				return llm.ErrConflict
			}
		}
		row.Open, row.ConnectionStatus = choice.Open, choice.Status
		if choice.Account != "" {
			v.AccountModels[choice.Account] = append(v.AccountModels[choice.Account], row)
		} else {
			v.DirectModels = append(v.DirectModels, row)
		}
	}
	if projected.SuppressNotice {
		p.Notice = ""
	}

	return nil
}

var errPageNotFound = errors.New("page not found")

// A successful probe is not activation: candidates remain unverified in their
// profile row until Select commits. Presentation reads existing proof metadata;
// Control.Select still validates live credentials and every frozen version.
func (s *Server) profileVerified(ctx context.Context, p llm.Profile) bool {
	if p.ProviderKind == "openai_account" {
		return p.Status == "ready"
	}
	if s.Control.Store == nil || s.Control.BridgeIdentity == "" || s.Control.BridgeError != nil {
		return false
	}
	record, err := s.Control.Store.Probe(ctx, p.ProfileID)
	if err != nil || record.ProfileVersion != p.Version || record.BridgeIdentity != s.Control.BridgeIdentity || record.Result.ValidateCapabilities(p.Capabilities) != nil {
		return false
	}
	version := int64(0)
	if p.AuthConnectionID != "" {
		a, e := s.Control.Store.Auth(ctx, p.AuthConnectionID)
		if e != nil || a.Status != "connected" ||
			a.AuthKind != "chatgpt_oauth" && a.ExpiresAt != nil && !a.ExpiresAt.After(time.Now()) {
			return false
		}
		version = a.Version
	}
	return record.AuthVersion == version
}

func (s *Server) projectModelSettings(ctx context.Context, facts modelsettings.SettingsFacts) (modelsettings.SettingsProjection, error) {
	if s.ModelSettings != nil {
		out, managed, err := s.ModelSettings(ctx, facts)
		if err != nil || managed {
			if err == nil && managed && facts.WithView && (out.View == nil || out.Copy == nil) {
				return out, llm.ErrConflict
			}
			return out, err
		}
	}
	return legacyModelSettingsV1(facts), nil
}
