package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	rels  = `<Relationships xmlns="` + relationshipsNS + `"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
	types = `<Types xmlns="` + contentTypesNS + `"><Override PartName="/word/document.xml" ContentType="` + mainContentType + `"/></Types>`
	start = `<w:document xmlns:w="` + wordNS + `"><w:body>`
	end   = `</w:body></w:document>`
)

func fixture(t *testing.T, relationships, content, body string, extra ...[2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, file := range append([][2]string{{"_rels/.rels", relationships}, {"[Content_Types].xml", content}, {mainPart, body}}, extra...) {
		w, err := z.Create(file[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(file[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestTextPreservesBodyOrderAndIgnoresOtherParts(t *testing.T) {
	body := "\xef\xbb\xbf" + `<?xml version="1.0" encoding="UTF-8"?>` + start +
		`<w:p><w:r><w:t>甲 &amp; 乙</w:t><w:tab/><w:t>🧭</w:t><w:br/></w:r><w:hyperlink><w:r><w:t>Link label</w:t></w:r></w:hyperlink></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>one</w:t></w:r></w:p><w:p><w:r><w:t>two</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>three</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p><w:r><w:drawing><w:p><w:r><w:t>ignored</w:t></w:r></w:p></w:drawing><w:t>finish</w:t></w:r></w:p>` + end
	got, err := Text(context.Background(), bytes.NewReader(fixture(t, rels, types, body, [2]string{"word/header1.xml", "hidden"})))
	if err != nil || got != "甲 & 乙\t🧭\nLink label\none\ntwo\tthree\nfinish" {
		t.Fatal(got, err)
	}
}

func TestTextRejectsAmbiguityMalformedAndBudgetOverruns(t *testing.T) {
	for _, tc := range []struct {
		name, relationships, content, body string
		extra                              [][2]string
		want                               error
	}{
		{"redirect", strings.Replace(rels, mainPart, "word/else.xml", 1), types, start + end, nil, ErrInvalid},
		{"external", strings.Replace(rels, "Target=", `TargetMode="External" Target=`, 1), types, start + end, nil, ErrInvalid},
		{"duplicate relationship", strings.Replace(rels, "</Relationships>", strings.TrimSuffix(rels[strings.Index(rels, "<Relationship Id"):], "</Relationships>")+"</Relationships>", 1), types, start + end, nil, ErrInvalid},
		{"duplicate part", rels, types, start + end, [][2]string{{"WORD/document.xml", "alias"}}, ErrInvalid},
		{"macro main", rels, strings.Replace(types, mainContentType, "application/vnd.ms-word.document.macroEnabled.main+xml", 1), start + end, nil, ErrInvalid},
		{"doctype", rels, types, `<!DOCTYPE w:document [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + start + end, nil, ErrInvalid},
		{"malformed", rels, types, start, nil, ErrInvalid},
		{"multiple roots", rels, types, start + end + start + end, nil, ErrInvalid},
		{"depth", rels, types, start + strings.Repeat("<w:x>", maxDepth) + strings.Repeat("</w:x>", maxDepth) + end, nil, ErrLimit},
		{"XML bytes", rels, types, start + strings.Repeat(" ", maxMainBytes) + end, nil, ErrLimit},
		{"text bytes", rels, types, start + "<w:p><w:r><w:t>" + strings.Repeat("a", maxText+1) + "</w:t></w:r></w:p>" + end, nil, ErrLimit},
		{"expanded bytes", rels, types, start + end, [][2]string{{"word/ignored.bin", strings.Repeat(" ", maxExpanded)}}, ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Text(context.Background(), bytes.NewReader(fixture(t, tc.relationships, tc.content, tc.body, tc.extra...)))
			if got != "" || !errors.Is(err, tc.want) {
				t.Fatal(len(got), err)
			}
		})
	}
}

func TestContentControlsPreserveVisibleOrderAndNativeHistory(t *testing.T) {
	run := func(s string) string { return "<w:r><w:t>" + s + "</w:t></w:r>" }
	par := func(s string) string { return "<w:p>" + s + "</w:p>" }
	wrap := func(s string) string {
		return "<w:sdt><w:sdtPr>" + par(run("private tag")) + "</w:sdtPr><w:sdtContent>" + s + "</w:sdtContent></w:sdt>"
	}
	body := start + par(run("A")) + wrap(wrap(par(run("B")+wrap(run("C"))))) +
		"<w:tbl>" + wrap("<w:tr><w:tc>"+par(run("D"))+"</w:tc>"+wrap("<w:tc>"+par(run("E"))+"</w:tc>")+"</w:tr>") + "</w:tbl>" +
		par("<w:del>"+wrap(run("deleted"))+"</w:del>"+run("F")) + end
	data := fixture(t, rels, types, body)
	got, err := TextWithContentControls(context.Background(), bytes.NewReader(data))
	if err != nil || got != "A\nBC\nD\tE\nF" {
		t.Fatal(got, err)
	}
	got, err = Text(context.Background(), bytes.NewReader(data))
	if err != nil || got != "A\n\nF" {
		t.Fatal("historical reader", got, err)
	}
}
