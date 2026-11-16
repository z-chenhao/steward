package web

import (
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"html/template"
	"strings"
	"text/template/parse"
)

func compileAppMarkup(source contract.MarkupSource, cardProgram, sidebarProgram []string) (*appMarkup, error) {
	cardFields := map[string]string{
		"begin-card": "Slug", "begin-summary": "URL Slug", "icon": "Icon", "begin-copy": "", "name": "Name", "description": "Description", "end-copy": "", "end-summary": "", "manage": "URL Slug ManageHidden ManageLabel Name More", "connect": "Unavailable URL Slug AddHidden AboutLabel Name Plus InterfaceReference ConnectLabel", "end-card": "",
	}
	sideFields := map[string]string{"begin-entry": "URL Slug", "icon": "Icon", "name": "Name", "pending": "Pending PendingLabel", "end-entry": ""}
	card, err := parsePassiveAppAtoms(source.Card, "app-card-atom-", cardFields)
	if err != nil {
		return nil, err
	}
	side, err := parsePassiveAppAtoms(source.Sidebar, "app-sidebar-atom-", sideFields)
	if err != nil {
		return nil, err
	}
	compiled := &appMarkup{card: card, sidebar: side}
	if err := preflightAppMarkup(compiled, cardProgram, sidebarProgram); err != nil {
		return nil, err
	}
	return compiled, nil
}
func parsePassiveAppAtoms(source, prefix string, fields map[string]string) (*template.Template, error) {
	return parsePassiveAppAtomsLimit(source, prefix, fields, 4096)
}

func parsePassiveAppAtomsLimit(source, prefix string, fields map[string]string, limit int) (*template.Template, error) {
	bad := connectedapps.ErrUnavailable
	if len(source) == 0 || len(source) > limit {
		return nil, bad
	}
	t, err := template.New("atoms").Parse(source)
	if err != nil {
		return nil, bad
	}
	if strings.TrimSpace(t.Tree.Root.String()) != "" || len(t.Templates()) != len(fields)+1 {
		return nil, bad
	}
	count := 0
	for op, allowed := range fields {
		atom := t.Lookup(prefix + op)
		if atom == nil || atom.Tree == nil || len(atom.Tree.Root.Nodes) == 0 {
			return nil, bad
		}
		var visit func(parse.Node, int) bool
		field := func(p *parse.PipeNode, boolean bool) bool {
			if p == nil || p.IsAssign || len(p.Decl) != 0 || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
				return false
			}
			f, ok := p.Cmds[0].Args[0].(*parse.FieldNode)
			if !ok || len(f.Ident) != 1 || !strings.Contains(" "+allowed+" ", " "+f.Ident[0]+" ") {
				return false
			}
			return !boolean || strings.Contains(" ManageHidden AddHidden Unavailable Pending HasIcon FirstHidden AdditionalHidden AdvancedOpen DiscoverCurrent ConnectedCurrent DirectoryCurrent CollectionPresent CategoriesPresent PaginationPresent OptionSelected Selected SearchVisible EmptyHidden Localized ManagedPresent HasInterface GroupsPresent HasURL ", " "+f.Ident[0]+" ")
		}
		visit = func(n parse.Node, depth int) bool {
			if n == nil {
				return true
			}
			count++
			if count > 512 || depth > 4 {
				return false
			}
			switch n := n.(type) {
			case *parse.ListNode:
				if n == nil {
					return true
				}
				for _, child := range n.Nodes {
					if !visit(child, depth) {
						return false
					}
				}
				return true
			case *parse.TextNode:
				return true
			case *parse.ActionNode:
				return field(n.Pipe, false)
			case *parse.IfNode:
				return field(n.Pipe, true) && visit(n.List, depth+1) && visit(n.ElseList, depth+1)
			default:
				return false
			}
		}
		if !visit(atom.Tree.Root, 0) {
			return nil, bad
		}
	}
	return t, nil
}
