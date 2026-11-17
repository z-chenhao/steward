package tui

import (
	"context"
	"fmt"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/contract"
	"github.com/z-chenhao/assistant/internal/plugin/terminalinterface/legacyv1"
	"reflect"
	"strings"
	"unicode"
)

func (u *terminal) words() contract.Copy {
	if u.view != nil {
		return *u.view
	}
	if u.Interface == nil {
		return legacyv1.View()
	}
	return contract.Copy{}
}
func (u *terminal) prepareView(ctx context.Context) error {
	if u.view != nil {
		return nil
	}
	if u.Interface == nil {
		v := legacyv1.View()
		u.view = &v
		return nil
	}
	result, managed, err := u.Interface(ctx, contract.Query{Operation: "view"})
	if err != nil {
		return llm.ProviderUnavailable
	}
	if !managed {
		v := legacyv1.View()
		u.view = &v
		return nil
	}
	if result.View == nil || result.Choice != nil || !validTerminalCopy(*result.View) {
		return llm.ProviderUnavailable
	}
	u.interfaceManaged = true
	u.view = result.View
	return nil
}
func validTerminalCopy(copy contract.Copy) bool {
	value := reflect.ValueOf(copy)
	typ := value.Type()
	total := 0
	for i := 0; i < value.NumField(); i++ {
		text := value.Field(i).String()
		total += len(text)
		if text == "" || len(text) > 4096 || strings.ContainsFunc(text, func(c rune) bool { return unicode.IsControl(c) && c != '\n' }) {
			return false
		}
		format := ""
		for j := 0; j < len(text); j++ {
			if text[j] == '%' {
				j++
				if j >= len(text) || (text[j] != 's' && text[j] != 'd') {
					return false
				}
				format += text[j : j+1]
			}
		}
		if format != typ.Field(i).Tag.Get("format") {
			return false
		}
	}
	return total <= 32768
}
func (u *terminal) choose(ctx context.Context, f contract.ChoiceFacts) (contract.Choice, error) {
	if u.Interface == nil {
		return legacyv1.Choose(f), nil
	}
	result, managed, err := u.Interface(ctx, contract.Query{Operation: "choose-model", Facts: &f})
	if err != nil {
		return contract.Choice{}, llm.ProviderUnavailable
	}
	if !managed {
		if u.interfaceManaged {
			return contract.Choice{}, llm.ProviderUnavailable
		}
		return legacyv1.Choose(f), nil
	}
	if result.Choice == nil || result.View != nil {
		return contract.Choice{}, llm.ProviderUnavailable
	}
	c := *result.Choice
	switch c.Kind {
	case "retry":
		return c, nil
	case "manual":
		if f.Manual && !f.Empty {
			return c, nil
		}
	case "index":
		if c.Index >= 0 && c.Index < f.Count && (c.Index == f.Exact || c.Index == f.Number || (f.Empty && f.Count == 1 && c.Index == 0)) {
			return c, nil
		}
	}
	return contract.Choice{}, fmt.Errorf("%w: invalid terminal choice", llm.ErrConflict)
}
