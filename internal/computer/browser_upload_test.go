package computer

import (
	"strings"
	"testing"
)

func TestComputerBrowserUploadPreservesFilenameDomainAndClosedParameters(t *testing.T) {
	name := strings.Repeat("长", 100) + ".txt"
	args := []any{map[string]any{"selector": "#upload", "file": map[string]any{"path": "imports/" + name, "computerId": "private", "name": "ignored-file-field"}}}
	budget := 3000
	p := ResolveTransferPresentation("playwright.upload", args, ActionParameters{Selector: "#upload", TimeoutMS: &budget, NameFromSource: true, MediaType: "application/octet-stream"})
	if p.Name != name || !ValidActionParameters("playwright.upload", "tab", args, p) {
		t.Fatal("source name was truncated or restricted as an Artifact name", p)
	}
	request := BrowserUploadRequest{Path: "imports/" + name, Navigation: "original", Selector: p.Selector, TimeoutMS: budget, Name: p.Name, MediaType: p.MediaType}
	if request.Validate() != nil {
		t.Fatal("fixed Driver narrowed the original filename domain")
	}
	q := p
	q.Name = "different"
	if ValidActionParameters("playwright.upload", "tab", args, q) {
		t.Fatal("source-name proof replaced")
	}
	q = p
	q.NameFromSource = false
	q.Name = "chosen.txt"
	q.MediaType = "text/plain"
	if !ValidActionParameters("playwright.upload", "tab", args, q) {
		t.Fatal("approved literal metadata unavailable")
	}
	for _, bad := range []string{"../escape", "a/b", "a\\b", "\x00name", "a\nname"} {
		q.Name = bad
		if ValidActionParameters("playwright.upload", "tab", args, q) {
			t.Fatal("metadata became a resource path", bad)
		}
	}
	q = p
	q.Value = "unapproved"
	if ValidActionParameters("playwright.upload", "tab", args, q) {
		t.Fatal("unused field became another effect")
	}
	q = p
	if ValidActionParameters("playwright.count", "tab", args, q) {
		t.Fatal("upload fields entered a read operation")
	}
	q = p
	q.MediaType = "bad\r\nheader"
	if ValidActionParameters("playwright.upload", "tab", args, q) {
		t.Fatal("invalid MIME accepted")
	}
}
