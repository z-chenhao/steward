package connectedapps

import (
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"strings"
	"unicode/utf8"
)

type CardLabel struct{ Name, Initial string }

func validLabelPolicy(value *contract.LabelPolicy) bool {
	if value == nil {
		return false
	}
	for _, plan := range []contract.LabelPlan{value.Named, value.Unnamed} {
		if plan.NameSource != "name" && plan.NameSource != "slug" {
			return false
		}
	}
	return true
}

// Label projects an already accepted presence-indexed plan. Source strings stay
// in the owner; only the fixed original field/first-rune operations run here.
func (s DirectorySelection) Label(name, slug string) (CardLabel, error) {
	if s.mode == directoryUnavailable || !validLabelPolicy(s.Labels) {
		return CardLabel{}, ErrUnavailable
	}
	plan := s.Labels.Unnamed
	if name != "" {
		plan = s.Labels.Named
	}
	var result CardLabel
	switch plan.NameSource {
	case "name":
		result.Name = name
	case "slug":
		result.Name = slug
	default:
		return CardLabel{}, ErrUnavailable
	}
	if result.Name != "" {
		r, _ := utf8.DecodeRuneInString(result.Name)
		result.Initial = string(r)
		if plan.UpperInitial {
			result.Initial = strings.ToUpper(result.Initial)
		}
	}
	return result, nil
}
