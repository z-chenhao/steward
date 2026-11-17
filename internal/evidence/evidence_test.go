package evidence

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOwnerDocxCursorCannotChangeMainReader(t *testing.T) {
	revision := "sha256:" + strings.Repeat("a", 64)
	raw, _ := json.Marshal(Request{Ref: "artifact:document", ReaderRevision: revision, Offset: 4, Limit: 32})
	if q, err := ParseOwner(string(raw)); err != nil || q.ReaderRevision != revision {
		t.Fatal(q, err)
	}
	if _, err := Parse(string(raw)); err == nil {
		t.Fatal("Main gained an operator interpreter selector")
	}
	for _, value := range []string{"sha256:" + strings.Repeat("g", 64), "/private/host/file", revision + "x"} {
		raw, _ = json.Marshal(Request{Ref: "artifact:document", ReaderRevision: value})
		if _, err := ParseOwner(string(raw)); err == nil {
			t.Fatal("invalid reader identity accepted", value)
		}
	}
	raw, _ = json.Marshal(Request{Ref: "conversation:message", ReaderRevision: revision})
	if _, err := ParseOwner(string(raw)); err == nil {
		t.Fatal("document cursor applied to another source kind")
	}
}

func TestMemoryEvidenceReferenceIsOwnerOnly(t *testing.T) {
	const valid = `{"ref":"memory:fact","offset":4,"limit":32,"links_offset":20}`
	if q, err := ParseOwner(valid); err != nil || q.Ref != "memory:fact" || q.Offset != 4 || q.Limit != 32 || q.LinksOffset != 20 {
		t.Fatal(q, err)
	}
	if _, err := Parse(valid); err == nil || ValidRef("memory:fact") {
		t.Fatal("Main gained the owner-only evidence reference")
	}
	for _, raw := range []string{`{"ref":"memory:"}`, `{"ref":"memory:../fact"}`, `{"ref":"memory:fact","user_id":"forged"}`, `{"ref":"memory:fact","offset":null}`} {
		if _, err := ParseOwner(raw); err == nil {
			t.Fatal("invalid owner evidence request accepted", raw)
		}
	}
}

func TestGitHubEvidenceReferenceIsCanonicalAndOwnerOnly(t *testing.T) {
	const valid = `{"ref":"github:42"}`
	if _, err := ParseOwner(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(valid); err == nil || ValidRef("github:42") {
		t.Fatal("GitHub evidence escaped owner-only parsing")
	}
	for _, raw := range []string{`{"ref":"github:0"}`, `{"ref":"github:042"}`, `{"ref":"github:+42"}`, `{"ref":"github:-42"}`, `{"ref":"github:9223372036854775808"}`} {
		if _, err := ParseOwner(raw); err == nil {
			t.Fatal("noncanonical observation reference", raw)
		}
	}
}

func TestEvidenceRequestAndUTF8Windows(t *testing.T) {
	if q, err := Parse(`{"kind":"conversation","ref":"message-id","limit":32}`); err != nil || q.Ref != "conversation:message-id" {
		t.Fatal("ordinary ID did not resolve in its explicit kind", q, err)
	}
	for _, raw := range []string{`{"ref":"message-id"}`, `{"kind":"artifact","ref":"conversation:message-id"}`, `{"kind":"memory","ref":"secret"}`, `{"kind":"task","ref":"current"}`} {
		if _, err := Parse(raw); err == nil {
			t.Fatal("ambiguous or unauthorized ID accepted", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"ref":"current","ref":"task:x"}`, `{"ref":"current","limit":null}`, `{"ref":"memory:x"}`, `{"ref":"task:a/b"}`, `{"ref":"current","scope":"all"}`, `{"ref":"current","limit":3}`, `{"ref":"current","offset":-1}`, `{"ref":"current"} {}`} {
		if _, err := Parse(raw); err == nil {
			t.Fatal("invalid evidence request accepted", raw)
		}
	}
	text := strings.Repeat("汉字🧭", 1000)
	q := Request{Ref: "conversation:opaque", Limit: 4096}
	var joined strings.Builder
	for {
		var page Page
		if err := Window(&page, text, q); err != nil || !utf8.ValidString(page.Text) || len(page.Text) > 4096 {
			t.Fatal(page, err)
		}
		joined.WriteString(page.Text)
		if page.NextOffset == nil {
			break
		}
		if *page.NextOffset <= q.Offset {
			t.Fatal("window made no progress")
		}
		q.Offset = *page.NextOffset
	}
	if joined.String() != text {
		t.Fatal("source bytes changed across UTF-8 windows")
	}
	if err := Window(&Page{}, text, Request{Offset: 1, Limit: 4096}); err == nil {
		t.Fatal("accepted a cursor inside UTF-8 character")
	}
}

func TestOwnerPageRevisionCannotChangeMainReader(t *testing.T) {
	revision := "sha256:" + strings.Repeat("a", 64)
	for _, ref := range []string{"conversation:message", "task:task", "artifact:file", "memory:fact", "github:42"} {
		raw, _ := json.Marshal(Request{Ref: ref, PageRevision: revision})
		if q, err := ParseOwner(string(raw)); err != nil || q.PageRevision != revision {
			t.Fatal(q, err)
		}
		if _, err := Parse(string(raw)); err == nil {
			t.Fatal("Main could select an owner page interpreter", ref)
		}
	}
	for _, value := range []string{revision + "x", "sha256:" + strings.Repeat("G", 64), "/private/source"} {
		raw, _ := json.Marshal(Request{Ref: "task:x", PageRevision: value})
		if _, err := ParseOwner(string(raw)); err == nil {
			t.Fatal("invalid page revision accepted", value)
		}
	}
}
