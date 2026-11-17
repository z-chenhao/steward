package service

import (
	"errors"
	"path/filepath"
	"strings"
)

// RestartRecipe is a private, bounded recipe; it grants no process/effect port.
// Actual death, human intent, fatal health and durable attempt admission remain
// independent control facts in Manager.
type RestartRecipe struct {
	Attempts     uint8  `json:"attempts"`
	RetryDelayMS uint32 `json:"retry_delay_ms"`
}
type RestartReceipt struct {
	GraphRevision string        `json:"graph_revision"`
	Recipe        RestartRecipe `json:"recipe"`
}

const recipePending = "pending"
const recipeAccepted = "accepted"
const recipeUnavailable = "unavailable"

func validRestartReceipt(r RestartReceipt) bool {
	return len(r.GraphRevision) == 71 && strings.HasPrefix(r.GraphRevision, "sha256:") && strings.Trim(r.GraphRevision[7:], "0123456789abcdef") == "" && r.Recipe.RetryDelayMS > 0
}
func (s settings) restartRecipe() (RestartRecipe, bool) {
	if s.Version == 1 {
		return legacyRestartRecipeV1(), true
	}
	if s.RecipeStatus == recipeAccepted && s.Recipe != nil {
		return s.Recipe.Recipe, true
	}
	return RestartRecipe{}, false
}

// Explicit pre-v2 installed deployments only; crash recovery retains that
// deployment. Explicit Start/Install converts it before managed admission.
// Never used for v2 absence.
func legacyRestartRecipeV1() RestartRecipe { return RestartRecipe{Attempts: 3, RetryDelayMS: 60000} }

func (s *Session) NeedsRestartRecipe() bool { return s.recipePending }

// FreezeRestartRecipe is called once while the background startup owns App.
// Inspect never opens domain databases or reinterprets an admitted deployment
// using a newer candidate. Missing/failed producers close automatic restart.
func (s *Session) FreezeRestartRecipe(receipt *RestartReceipt) error {
	control, err := s.manager.lock("control.lock")
	if err != nil {
		return err
	}
	defer control.Close()
	state, err := s.manager.load()
	if err != nil {
		return err
	}
	if !state.Installed || !state.Enabled {
		return ErrStopped
	}
	if state.Version != 2 || state.Deployment != s.deployment {
		return errors.New("service deployment changed")
	}
	if receipt != nil && !validRestartReceipt(*receipt) {
		return errors.New("invalid service restart recipe")
	}
	if state.RecipeStatus != recipePending {
		if receipt == nil && state.RecipeStatus == recipeUnavailable || receipt != nil && state.Recipe != nil && *receipt == *state.Recipe {
			s.recipePending = false
			return nil
		}
		return errors.New("service restart recipe already frozen")
	}
	state.RecipeStatus = recipeUnavailable
	if receipt != nil {
		frozen := *receipt
		state.Recipe = &frozen
		state.RecipeStatus = recipeAccepted
	}
	if err := s.manager.save(state); err != nil {
		return err
	}
	s.recipePending = false
	return nil
}
func (m *Manager) readHeartbeat(s settings, h *heartbeat) error {
	if err := readJSON(filepath.Join(m.dir(), "heartbeat.json"), h); err != nil {
		return err
	}
	if s.Version == 2 && h.Deployment != s.Deployment {
		return errors.New("heartbeat belongs to another deployment")
	}
	return nil
}
