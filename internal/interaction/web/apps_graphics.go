package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	xhtml "golang.org/x/net/html"
	"html/template"
	"math"
	"strconv"
	"strings"
)

type appGraphicData struct {
	Initial, IconURL string
	HasIcon          bool
}

func appGraphicsAvailable(v *appsView) bool {
	return v == nil || v.Interface == nil || !v.Interface.graphicsManaged || v.Interface.markup != nil && v.Interface.markup.graphics != nil
}
func UnavailableAppGraphicMarkup(p AppInterfacePolicy) AppInterfacePolicy {
	p = UnavailableAppMarkup(p)
	p.graphicsManaged = true
	return p
}
func ManagedAppGraphicMarkup(p AppInterfacePolicy, m contract.Markup) (AppInterfacePolicy, error) {
	if !p.available() || p.native || len(m.Source) > 8192 {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	var source contract.GraphicMarkupSource
	if rsiplugin.Decode([]byte(m.Source), &source) != nil {
		return AppInterfacePolicy{}, connectedapps.ErrUnavailable
	}
	graphics, err := compileAppGraphics(source.Graphics)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	card, side := appMarkupPrograms(p)
	compiled, err := compileAppMarkup(contract.MarkupSource{Card: source.Card, Sidebar: source.Sidebar}, card, side)
	if err != nil {
		return AppInterfacePolicy{}, err
	}
	compiled.graphics = graphics
	p.markupManaged = true
	p.graphicsManaged = true
	p.markup = compiled
	return p, nil
}
func compileAppGraphics(source string) (*template.Template, error) {
	t, err := parsePassiveAppAtoms(source, "app-", map[string]string{"icon": "Initial IconURL HasIcon", "more": "", "plus": ""})
	if err != nil {
		return nil, err
	}
	seed := make([]byte, 16)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(seed)
	for _, hasIcon := range []bool{false, true} {
		d := appGraphicData{Initial: "initial_" + nonce + `<&'"中文`, IconURL: "/apps/logo/" + nonce + "?revision=original&icon=1", HasIcon: hasIcon}
		raw, err := renderAppGraphic(t, "app-icon", d)
		if err != nil || !validAppIcon(string(raw), d, nonce) {
			return nil, connectedapps.ErrUnavailable
		}
	}
	for _, name := range []string{"app-more", "app-plus"} {
		raw, err := renderAppGraphic(t, name, appGraphicData{})
		if err != nil || !validAppSymbol(string(raw)) {
			return nil, connectedapps.ErrUnavailable
		}
	}
	return t, nil
}
func renderAppGraphic(t *template.Template, name string, d appGraphicData) (template.HTML, error) {
	var out bytes.Buffer
	if err := t.ExecuteTemplate(&out, name, d); err != nil {
		return "", err
	}
	return template.HTML(out.String()), nil
}
func graphicRoot(source string) *xhtml.Node {
	nodes, err := markupNodes(source)
	if err != nil {
		return nil
	}
	var root *xhtml.Node
	for _, n := range nodes {
		if n.Type == xhtml.TextNode && strings.TrimSpace(n.Data) == "" {
			continue
		}
		if n.Type != xhtml.ElementNode || root != nil {
			return nil
		}
		root = n
	}
	return root
}
func validAppIcon(source string, d appGraphicData, nonce string) bool {
	root := graphicRoot(source)
	if root == nil || root.Data != "span" || markupAttr(root, "class") != "app-logo" || markupAttr(root, "aria-hidden") != "true" {
		return false
	}
	elements := markupAll(root, func(n *xhtml.Node) bool { return true })
	if len(elements) != map[bool]int{true: 3, false: 2}[d.HasIcon] {
		return false
	}
	children := []*xhtml.Node{}
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		children = append(children, c)
	}
	if len(children) != map[bool]int{true: 2, false: 1}[d.HasIcon] || children[0].Data != "span" || markupText(children[0], nil) != d.Initial {
		return false
	}
	for _, n := range elements {
		seen := map[string]bool{}
		for _, a := range n.Attr {
			if a.Namespace != "" || seen[a.Key] {
				return false
			}
			seen[a.Key] = true
			if strings.HasPrefix(a.Key, "data-plugin-") {
				if strings.Contains(a.Val, nonce) {
					return false
				}
				continue
			}
			if n == root {
				if a.Key != "class" && a.Key != "aria-hidden" {
					return false
				}
			} else if n == children[0] {
				return false
			} else if !strings.Contains(" src alt width height loading decoding ", " "+a.Key+" ") {
				return false
			}
		}
	}
	if d.HasIcon {
		img := children[1]
		if img.Data != "img" || markupAttr(img, "src") != d.IconURL || markupAttr(img, "alt") != "" || !markupHas(img, "alt") || markupAttr(img, "width") != "36" || markupAttr(img, "height") != "36" || markupAttr(img, "loading") != "lazy" || markupAttr(img, "decoding") != "async" {
			return false
		}
	}
	return true
}

// A decorative passive vector may change geometry,but never event handlers,
// references,external images,paint URLs,style,IDs or DOM hooks.
func validAppSymbol(source string) bool {
	root := graphicRoot(source)
	if root == nil || root.Data != "svg" || markupAttr(root, "width") != "20" || markupAttr(root, "height") != "20" || markupAttr(root, "viewBox") != "0 0 24 24" || markupAttr(root, "aria-hidden") != "true" {
		return false
	}
	nodes := markupAll(root, func(n *xhtml.Node) bool { return true })
	if len(nodes) < 2 || len(nodes) > 64 {
		return false
	}
	for _, n := range nodes {
		if n != root && (n.Data != "path" && n.Data != "circle") {
			return false
		}
		if n != root && n.Parent != root {
			return false
		}
		seen := map[string]bool{}
		for _, a := range n.Attr {
			if a.Namespace != "" || seen[a.Key] {
				return false
			}
			seen[a.Key] = true
			if strings.HasPrefix(a.Key, "data-plugin-") {
				continue
			}
			switch a.Key {
			case "width", "height", "viewBox", "aria-hidden":
				if n != root {
					return false
				}
			case "fill", "stroke":
				if a.Val != "none" && a.Val != "currentColor" {
					return false
				}
			case "stroke-width":
				v, err := strconv.ParseFloat(a.Val, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0.5 || v > 3 {
					return false
				}
			case "cx", "cy", "r":
				if n.Data != "circle" {
					return false
				}
				v, err := strconv.ParseFloat(a.Val, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 24 || a.Key == "r" && v == 0 {
					return false
				}
			case "d":
				if n.Data != "path" || len(a.Val) == 0 || len(a.Val) > 256 {
					return false
				}
				for _, r := range a.Val {
					if !strings.ContainsRune("MmLlHhVvCcSsQqTtAaZz0123456789+-. ,\t\n", r) {
						return false
					}
				}
			default:
				return false
			}
		}
		if n.Data == "path" && !markupHas(n, "d") {
			return false
		}
		if n.Data == "circle" && (!markupHas(n, "cx") || !markupHas(n, "cy") || !markupHas(n, "r")) {
			return false
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.TextNode && strings.TrimSpace(c.Data) != "" || c.Type == xhtml.CommentNode {
				return false
			}
		}
	}
	return true
}
