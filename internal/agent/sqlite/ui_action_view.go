package sqlite

import (
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

func bindAcceptedActionViews(widget *ui.Widget, views []contract.ActionView, version string) error {
	if !contract.ActionDisplayRequired(version) {
		return nil
	}
	expected := map[int]bool{}
	for i, c := range widget.Components {
		if c.Name == "steward.action" {
			expected[i] = true
		}
	}
	if len(expected) != len(views) {
		return agent.ErrStorage
	}
	for _, view := range views {
		if !expected[view.Component] {
			return agent.ErrStorage
		}
		for _, text := range []string{view.WorkingLabel, view.UnconfirmedError, view.UnknownError, view.CancelledError, view.NotExecutedError, view.FailedError} {
			if !utf8.ValidString(text) || strings.ContainsRune(text, 0) || len([]rune(text)) < 1 || len([]rune(text)) > 300 {
				return agent.ErrStorage
			}
		}
		widget.Components[view.Component].ActionManaged = true
		copy := view
		widget.Components[view.Component].ActionView = &copy
		delete(expected, view.Component)
	}
	return nil
}
