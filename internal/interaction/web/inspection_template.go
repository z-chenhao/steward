package web

import (
	"fmt"
	"html/template"
	"strings"
	"text/template/parse"
)

// Private Host template validation shared by actual owning views. Each consumer
// names its display fields, functions and shared atoms; there is no public UI DSL.
type inspectionTemplateSpec struct {
	root, fields, functions string
	names, shared           map[string]bool
	variables               bool
	privateName             func(string) bool
	unavailable             error
}

func compileInspectionView(source string, functions template.FuncMap, host *template.Template, spec inspectionTemplateSpec) (*template.Template, error) {
	bad := spec.unavailable
	if len(source) == 0 || len(source) > 65536 {
		return nil, bad
	}
	t, err := template.New(spec.root).Funcs(functions).Parse(source)
	if err != nil {
		return nil, bad
	}
	if t.Tree == nil || strings.TrimSpace(t.Tree.Root.String()) != "" {
		return nil, bad
	}
	required := spec.names
	ownerName := func(name string) bool { return required[name] || spec.privateName != nil && spec.privateName(name) }
	if spec.privateName == nil {
		// Memory retains its original exact template contract.
		if len(t.Templates()) != len(required)+1 {
			return nil, bad
		}
	} else {
		// Trace fixes only actual Host entries. Owner-private implementation
		// names may change independently, but cannot redefine shared atoms.
		own := map[string]bool{}
		for _, part := range t.Templates() {
			if part.Name() == spec.root {
				continue
			}
			if !ownerName(part.Name()) {
				return nil, bad
			}
			own[part.Name()] = true
		}
		for name := range required {
			if !own[name] {
				return nil, bad
			}
		}
		spec.names = own
	}
	count := 0
	reason := ""
	edges := map[string][]string{}
	var pipe func(*parse.PipeNode) bool
	var arg func(parse.Node) bool
	field := func(names []string) bool {
		for _, name := range names {
			if !strings.Contains(" "+spec.fields+" ", " "+name+" ") {
				reason = "field " + name
				return false
			}
		}
		return true
	}
	arg = func(n parse.Node) bool {
		switch n := n.(type) {
		case *parse.FieldNode:
			return field(n.Ident)
		case *parse.VariableNode:
			return len(n.Ident) > 0 && (n.Ident[0] == "$" || spec.variables) && field(n.Ident[1:])
		case *parse.IdentifierNode:
			if !strings.Contains(spec.functions, " "+n.Ident+" ") {
				reason = "function " + n.Ident
				return false
			}
			return true
		case *parse.PipeNode:
			return pipe(n)
		case *parse.ChainNode:
			return arg(n.Node) && field(n.Field)
		case *parse.StringNode, *parse.NumberNode, *parse.BoolNode, *parse.NilNode, *parse.DotNode:
			return true
		}
		return false
	}
	pipe = func(p *parse.PipeNode) bool {
		if p == nil || (!spec.variables && (p.IsAssign || len(p.Decl) != 0)) || len(p.Cmds) > 8 {
			return false
		}
		for _, c := range p.Cmds {
			for _, a := range c.Args {
				if !arg(a) {
					return false
				}
			}
		}
		return true
	}
	for name := range spec.names {
		view := t.Lookup(name)
		if view == nil || view.Tree == nil {
			return nil, bad
		}
		var visit func(parse.Node, int) bool
		visit = func(n parse.Node, ranges int) bool {
			if n == nil {
				return true
			}
			count++
			if count > 4096 || ranges > 3 {
				return false
			}
			switch n := n.(type) {
			case *parse.ListNode:
				if n == nil {
					return true
				}
				for _, c := range n.Nodes {
					if !visit(c, ranges) {
						return false
					}
				}
				return true
			case *parse.TextNode:
				return true
			case *parse.ActionNode:
				return pipe(n.Pipe)
			case *parse.IfNode:
				return pipe(n.Pipe) && visit(n.List, ranges) && visit(n.ElseList, ranges)
			case *parse.WithNode:
				return pipe(n.Pipe) && visit(n.List, ranges) && visit(n.ElseList, ranges)
			case *parse.RangeNode:
				return pipe(n.Pipe) && visit(n.List, ranges+1) && visit(n.ElseList, ranges+1)
			case *parse.TemplateNode:
				if !spec.names[n.Name] && !spec.shared[n.Name] {
					return false
				}
				if spec.names[n.Name] {
					edges[name] = append(edges[name], n.Name)
				}
				return n.Pipe == nil || pipe(n.Pipe)
			}
			return false
		}
		if !visit(view.Tree.Root, 0) {
			return nil, fmt.Errorf("%w: view %s %s count=%d", bad, name, reason, count)
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var acyclic func(string) bool
	acyclic = func(name string) bool {
		if visiting[name] {
			return false
		}
		if done[name] {
			return true
		}
		visiting[name] = true
		for _, child := range edges[name] {
			if !acyclic(child) {
				return false
			}
		}
		visiting[name] = false
		done[name] = true
		return true
	}
	for name := range spec.names {
		if !acyclic(name) {
			return nil, bad
		}
	}
	// Shared trees remain Host authored. Candidates cannot redefine their names
	// or directly reference arbitrary layout/templates outside the small allowlist.
	for _, shared := range host.Templates() {
		if shared.Name() == spec.root || ownerName(shared.Name()) {
			continue
		}
		if _, err = t.AddParseTree(shared.Name(), shared.Tree); err != nil {
			return nil, bad
		}
	}
	return t, nil
}
