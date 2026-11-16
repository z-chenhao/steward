package web

import (
	"bytes"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"html/template"
)

func UnavailableAppCardView(p AppInterfacePolicy) AppInterfacePolicy {
	p.cardManaged = true
	p.cardView = nil
	return p
}
func ManagedAppCardView(p AppInterfacePolicy, v contract.CardView) (AppInterfacePolicy, error) {
	if !p.available() || p.native || !validAppCardProgram(v.Program) {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	v.Program = append([]string(nil), v.Program...)
	p.cardManaged = true
	p.cardView = &v
	return p, nil
}

// Complete, bounded vocabulary and exact parent scopes preserve all original
// controls. Order belongs to the contribution; URL/value/row authority does not.
func validAppCardProgram(program []string) bool {
	if len(program) != 11 {
		return false
	}
	parents := map[string]string{"begin-card": "", "begin-summary": "card", "begin-copy": "summary", "icon": "summary", "name": "copy", "description": "copy", "manage": "card", "connect": "card"}
	ends := map[string]string{"end-card": "card", "end-summary": "summary", "end-copy": "copy"}
	seen := map[string]bool{}
	stack := []string{}
	for i, op := range program {
		if seen[op] {
			return false
		}
		seen[op] = true
		if node, ok := ends[op]; ok {
			if len(stack) == 0 || stack[len(stack)-1] != node {
				return false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 && i != len(program)-1 {
				return false
			}
			continue
		}
		parent, ok := parents[op]
		if !ok {
			return false
		}
		current := ""
		if len(stack) > 0 {
			current = stack[len(stack)-1]
		}
		if current != parent {
			return false
		}
		switch op {
		case "begin-card":
			if i != 0 {
				return false
			}
			stack = append(stack, "card")
		case "begin-summary":
			stack = append(stack, "summary")
		case "begin-copy":
			stack = append(stack, "copy")
		}
	}
	return len(stack) == 0
}
func appCardsAvailable(view *appsView) bool {
	if view == nil || view.Selected != nil {
		return true
	}
	for _, group := range view.Groups {
		if len(group.Items) > 0 {
			p := view.Interface
			return appGraphicsAvailable(view) && (p == nil || !p.markupManaged || p.markup != nil) && (p == nil || !p.cardManaged || p.cardView != nil)
		}
	}
	return true
}

// The selected passive source consumes only a local display DTO. The named
// legacy namespace is limited to actual pre-markup/direct/captured references.
func renderAppCard(t *template.Template, view *appsView, card appCard, language string) (template.HTML, error) {
	p := view.Interface
	var program []string
	if p == nil || !p.cardManaged {
		program = appCardProgramLegacyV1()
	} else if p.cardView != nil {
		program = p.cardView.Program
	} else {
		return "", connectedapps.ErrUnavailable
	}
	if p != nil && p.markupManaged {
		if p.markup == nil {
			return "", connectedapps.ErrUnavailable
		}
		data, err := appCardDisplay(t, view, card, language)
		if err != nil {
			return "", err
		}
		return renderAppMarkupAtoms(p.markup.card, "app-card-atom-", program, data)
	}
	var out bytes.Buffer
	data := struct {
		Apps *appsView
		Card appCard
	}{view, card}
	for _, op := range program {
		if err := t.ExecuteTemplate(&out, "app-card-atom-"+op, data); err != nil {
			return "", err
		}
	}
	return template.HTML(out.String()), nil
}
