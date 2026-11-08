package web

// identifierView is shared display state, not a domain identifier or authority.
// The full value survives visual truncation; navigation remains a separate link.
type identifierView struct {
	Label string
	Value string
	URL   string
}

func identifier(label, value, url string) identifierView {
	return identifierView{Label: label, Value: value, URL: url}
}
