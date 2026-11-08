package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
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
	Choices       []composerModel `json:"choices"`
	Current       string          `json:"current"`
	ConfigVersion int64           `json:"config_version"`
	Unavailable   bool            `json:"unavailable,omitempty"`
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
	seen := map[string]bool{}
	for _, p := range profiles {
		if p.ProviderKind != "openai_account" && p.AuthConnectionID != "" {
			auth, e := s.Control.Store.Auth(ctx, p.AuthConnectionID)
			if e != nil || auth.Status != "connected" {
				continue
			}
		}
		key := profileKey(p)
		label := p.Name
		if p.ProviderKind == "openai_account" {
			_, ok := connected[p.AuthConnectionID]
			if !ok {
				continue
			}
			key = accountKey(p.AuthConnectionID, p.ModelID)
			label = p.ModelID
		} else if !s.profileVerified(ctx, p) && p.ProfileID != activeProfile(status) {
			continue
		}
		if p.ProfileID == activeProfile(status) {
			v.Current = key
		}
		if !seen[key] {
			v.Choices = append(v.Choices, composerModel{Key: key, Label: label})
			seen[key] = true
		}
	}
	if catalog && s.AccountCatalog != nil && s.Accounts != nil {
		// Iterate persisted account order, rather than map order, for stable menus.
		accounts, e := s.Accounts.Store.Accounts(ctx)
		if e != nil {
			return v, e
		}
		for _, a := range accounts {
			if _, ok := connected[a.ConnectionID]; !ok {
				continue
			}
			models, e := s.AccountCatalog(ctx, a.ConnectionID)
			if e != nil {
				v.Unavailable = true
				continue
			}
			available := map[string]bool{}
			for _, m := range models {
				available[accountKey(a.ConnectionID, m.ModelID)] = true
			}
			kept := v.Choices[:0]
			for _, choice := range v.Choices {
				if strings.HasPrefix(choice.Key, "account:"+a.ConnectionID+":") && !available[choice.Key] {
					if choice.Key != v.Current {
						continue
					}
					choice.Disabled = true
				}
				kept = append(kept, choice)
			}
			v.Choices = kept
			for _, m := range models {
				key := accountKey(a.ConnectionID, m.ModelID)
				if !seen[key] {
					label := m.ModelID
					v.Choices = append(v.Choices, composerModel{Key: key, Label: label})
					seen[key] = true
				}
			}
		}
	}
	// The same name order serves the native picker, catalog refresh and switch
	// response. Stable ties retain distinct account keys without exposing labels.
	sort.SliceStable(v.Choices, func(i, j int) bool {
		return strings.ToLower(v.Choices[i].Label) > strings.ToLower(v.Choices[j].Label)
	})
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
	} else {
		models, e := s.AccountCatalog(ctx, account)
		if e != nil {
			return e
		}
		if len(models) == 0 {
			return llm.ModelNotFound
		}
		p, err = s.accountModel(ctx, account, models[0].ModelID)
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
