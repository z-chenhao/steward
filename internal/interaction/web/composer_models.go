package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	menu "github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/legacyv1"
)

// These are private Web choices, not persisted profiles or provider contracts.
// Catalog reads never create profiles; a selected account model is materialized
// only at the existing versioned user-driven activation boundary.
type composerModel struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Disabled bool   `json:"disabled,omitempty"`
}
type composerModels struct {
	Picker         *menu.PickerPresentation `json:"picker"`
	ReaderRevision string                   `json:"reader_revision,omitempty"`
	Choices        []composerModel          `json:"choices"`
	Current        string                   `json:"current"`
	ConfigVersion  int64                    `json:"config_version"`
	Unavailable    bool                     `json:"unavailable,omitempty"`
}

func profileKey(p llm.Profile) string {
	return "profile:" + p.ProfileID + ":" + strconv.FormatInt(p.Version, 10)
}
func accountKey(account, model string) string { return "account:" + account + ":" + model }
func (s *Server) modelChoices(ctx context.Context, catalog bool) (composerModels, error) {
	v := composerModels{Choices: []composerModel{}}
	status, err := s.Core.WebStatus(ctx)
	if err != nil {
		return v, err
	}
	v.ConfigVersion = status.ConfigVersion
	if s.Models == nil {
		p := legacyv1.Picker()
		v.Picker = &p
		return v, nil
	}
	profiles, err := s.Models.ListProfiles(ctx)
	if err != nil {
		return v, err
	}
	connected := map[string]llm.AuthConnection{}
	if s.Accounts != nil {
		accounts, e := s.Accounts.Store.Accounts(ctx)
		if e != nil {
			return v, e
		}
		for _, a := range accounts {
			if a.Status == "connected" {
				connected[a.ConnectionID] = a
			}
		}
	}
	facts := menu.Facts{Candidates: []menu.Candidate{}, Catalogues: []menu.Catalogue{}}
	for _, p := range profiles {
		connectedProfile := true
		if p.ProviderKind == "openai_account" {
			_, connectedProfile = connected[p.AuthConnectionID]
		} else if p.AuthConnectionID != "" {
			auth, e := s.Control.Store.Auth(ctx, p.AuthConnectionID)
			connectedProfile = e == nil && auth.Status == "connected"
		}
		key := profileKey(p)
		name := p.Name
		if p.ProviderKind == "openai_account" {
			key = accountKey(p.AuthConnectionID, p.ModelID)
			name = ""
		}
		verified := false
		if connectedProfile && p.ProviderKind != "openai_account" {
			verified = s.profileVerified(ctx, p)
		}
		facts.Candidates = append(facts.Candidates, menu.Candidate{Key: key, Name: name, Model: p.ModelID, Account: p.AuthConnectionID, AccountBacked: p.ProviderKind == "openai_account", Connected: connectedProfile, Verified: verified, Current: p.ProfileID == activeProfile(status)})
	}
	if catalog && s.AccountCatalog != nil && s.Accounts != nil {
		accounts, e := s.Accounts.Store.Accounts(ctx)
		if e != nil {
			return v, e
		}
		for _, a := range accounts {
			if _, ok := connected[a.ConnectionID]; !ok {
				continue
			}
			models, e := s.AccountCatalog(ctx, a.ConnectionID)
			c := menu.Catalogue{Account: a.ConnectionID, Available: e == nil, Models: []menu.Model{}}
			if e == nil {
				for _, m := range models {
					c.Models = append(c.Models, menu.Model{Key: accountKey(a.ConnectionID, m.ModelID), Name: m.ModelID})
				}
			}
			facts.Catalogues = append(facts.Catalogues, c)
		}
	}
	var projected menu.Projection
	managed := false
	if s.ModelMenu != nil {
		projected, managed, err = s.ModelMenu(ctx, facts)
		if err != nil {
			return v, err
		}
	}
	if !managed {
		projected = legacyModelMenuV1(facts)
	}
	if err = validateModelMenu(facts, projected); err != nil {
		return v, err
	}
	for _, c := range projected.Choices {
		v.Choices = append(v.Choices, composerModel{Key: c.Key, Label: c.Label, Disabled: c.Disabled})
	}
	v.ReaderRevision = projected.ReaderRevision
	var picker menu.PickerPresentation
	if projected.ReaderRevision != "" {
		if s.ModelPicker == nil {
			return v, llm.ProviderUnavailable
		}
		picker, err = s.ModelPicker(ctx, projected.ReaderRevision)
		if err != nil {
			return v, err
		}
	} else {
		picker = legacyv1.Picker()
	} // Explicit never-managed/storeless embedding.
	if !validModelPicker(picker) {
		return v, llm.ProviderUnavailable
	}
	v.Picker = &picker
	v.Current, v.Unavailable = projected.Current, projected.Unavailable
	return v, nil
}
func (s *Server) composerModelsAPI(w http.ResponseWriter, r *http.Request) {
	v, err := s.modelChoices(r.Context(), true)
	if err != nil {
		fail(w, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	jsonResult(w, v)
}
func (s *Server) chooseComposerModel(w http.ResponseWriter, r *http.Request) error {
	if r.FormValue("consent") != "yes" {
		return llm.ErrConflict
	}
	if s.Models == nil || s.Control.Store == nil {
		return llm.ErrNoBridge
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	cv, err := strconv.ParseInt(r.FormValue("config_version"), 10, 64)
	if err != nil {
		return llm.ErrConflict
	}
	status, err := s.Core.WebStatus(r.Context())
	if err != nil {
		return err
	}
	if status.ConfigVersion != cv {
		return llm.ErrConflict
	}
	key := strings.SplitN(r.FormValue("choice"), ":", 3)
	if len(key) != 3 {
		return llm.ErrConflict
	}
	var profile llm.Profile
	switch key[0] {
	case "profile":
		pv, e := strconv.ParseInt(key[2], 10, 64)
		if e != nil {
			return llm.ErrConflict
		}
		profile, e = s.Control.Store.Profile(r.Context(), key[1])
		if e != nil {
			return e
		}
		if profile.Version != pv {
			return llm.ErrConflict
		}
	case "account":
		profile, err = s.accountModel(r.Context(), key[1], key[2])
		if err != nil {
			return err
		}
	default:
		return llm.ErrConflict
	}
	if err = s.Control.SelectWithProbe(r.Context(), profile.ProfileID, profile.Version, cv); err != nil {
		return err
	}
	if err = s.Core.WaitForRuntime(r.Context()); err != nil {
		return err
	}
	if r.Header.Get("Accept") == "application/json" {
		v, e := s.modelChoices(r.Context(), false)
		if e != nil {
			return e
		}
		jsonResult(w, v)
		return nil
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
	return nil
}
func (s *Server) accountModel(ctx context.Context, account, model string) (llm.Profile, error) {
	if s.AccountCatalog == nil {
		return llm.Profile{}, llm.ErrNoBridge
	}
	catalog, err := s.AccountCatalog(ctx, account)
	if err != nil {
		return llm.Profile{}, err
	}
	var metadata *llm.CatalogModel
	for i := range catalog {
		if catalog[i].ModelID == model {
			metadata = &catalog[i]
			break
		}
	}
	if metadata == nil {
		return llm.Profile{}, llm.ModelNotFound
	}
	profiles, err := s.Models.ListProfiles(ctx)
	if err != nil {
		return llm.Profile{}, err
	}
	for _, p := range profiles {
		if p.ProviderKind == "openai_account" && p.AuthConnectionID == account && p.ModelID == model {
			return p, nil
		}
	}
	return s.Models.AddProfile(ctx, llm.ProfileInput{Name: model, ProviderKind: "openai_account", WireAPI: "openai_responses", AuthConnectionID: account, ModelID: model, ContextWindow: metadata.ContextWindow, MaxOutputTokens: min(metadata.MaxOutputTokens, metadata.ContextWindow), Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true, ImageInput: metadata.ImageInput}})
}
func (s *Server) selectAccountModelAfterLogin(ctx context.Context, account string) error {
	if s.Models == nil || s.Control.Store == nil || s.Core == nil || s.AccountCatalog == nil {
		return nil
	}
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	status, err := s.Core.WebStatus(ctx)
	if err != nil {
		return err
	}
	var p llm.Profile
	if current := activeProfile(status); current != "" {
		p, err = s.Control.Store.Profile(ctx, current)
		if err != nil {
			return err
		}
		// Reauthorization invalidates profile readiness as well as credentials.
		// Restore only this account's current model; never replace another choice.
		if p.ProviderKind != "openai_account" || p.AuthConnectionID != account {
			return nil
		}
		policy, e := s.connectionDefaults(ctx, menu.ConnectionFacts{AccountBacked: true, Login: &menu.LoginFacts{Models: []string{}, RestoreOwnedCurrent: true}})
		if e != nil {
			return e
		}
		if policy.RestoreOwnedCurrent == nil || policy.LoginModel != "" {
			return llm.ErrConflict
		}
		if !*policy.RestoreOwnedCurrent {
			return nil
		}
	} else {
		models, e := s.AccountCatalog(ctx, account)
		if e != nil {
			return e
		}
		facts := menu.ConnectionFacts{AccountBacked: true, Login: &menu.LoginFacts{Models: []string{}}}
		for _, model := range models {
			facts.Login.Models = append(facts.Login.Models, model.ModelID)
		}
		policy, e := s.connectionDefaults(ctx, facts)
		if e != nil {
			return e
		}
		if policy.LoginModel == "" {
			return llm.ModelNotFound
		}
		known := false
		for _, model := range models {
			if model.ModelID == policy.LoginModel {
				known = true
				break
			}
		}
		if !known {
			return llm.ErrConflict
		}
		p, err = s.accountModel(ctx, account, policy.LoginModel)
		if err != nil {
			return err
		}
	}
	if err = s.Control.SelectWithProbe(ctx, p.ProfileID, p.Version, status.ConfigVersion); err != nil {
		return err
	}
	return s.Core.WaitForRuntime(ctx)
}

// Preserve JSON failures without turning a failed switch into a navigation that
// discards the composer draft. The generic form route owns native errors.
func (s *Server) composerModelAction(w http.ResponseWriter, r *http.Request) error {
	err := s.chooseComposerModel(w, r)
	if err != nil && r.Header.Get("Accept") == "application/json" {
		code := http.StatusUnprocessableEntity
		if errors.Is(err, llm.ErrConflict) {
			code = http.StatusConflict
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Model selection failed. Try again."})
		return nil
	}
	return err
}
