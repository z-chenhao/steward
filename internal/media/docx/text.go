// Package docx extracts ordinary body text from a bounded Word package.
package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

const (
	MediaType       = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	MaxArchiveBytes = 8 << 20
	maxExpanded     = 32 << 20
	maxMainBytes    = 2 << 20
	maxPackageXML   = 64 << 10
	maxText         = 256 << 10
	maxEntries      = 512
	maxDepth        = 64
	maxTokens       = 200000
	mainPart        = "word/document.xml"
	mainContentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
	relationshipsNS = "http://schemas.openxmlformats.org/package/2006/relationships"
	contentTypesNS  = "http://schemas.openxmlformats.org/package/2006/content-types"
	wordNS          = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	strictWordNS    = "http://purl.oclc.org/ooxml/wordprocessingml/main"
)

var (
	ErrInvalid = errors.New("invalid or unsupported DOCX document")
	ErrLimit   = errors.New("DOCX resource limit exceeded")
)

// Text never expands or opens external relationships or unrelated ZIP parts.
func Text(ctx context.Context, src io.Reader) (string, error) {
	return text(ctx, src, false)
}

// TextWithContentControls reads the visible content of structured document tags
// for managed readers. Text retains the historical native interpretation.
func TextWithContentControls(ctx context.Context, src io.Reader) (string, error) {
	return text(ctx, src, true)
}

func text(ctx context.Context, src io.Reader, controls bool) (string, error) {
	data, err := io.ReadAll(io.LimitReader(src, MaxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxArchiveBytes {
		return "", ErrLimit
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", ErrInvalid
	}
	if len(z.File) > maxEntries {
		return "", ErrLimit
	}
	parts := make(map[string]*zip.File, len(z.File))
	var expanded uint64
	for _, f := range z.File {
		if f.UncompressedSize64 > maxExpanded-expanded {
			return "", ErrLimit
		}
		expanded += f.UncompressedSize64
		key := strings.ToLower(f.Name)
		if _, exists := parts[key]; exists {
			return "", ErrInvalid
		}
		parts[key] = f
	}
	for _, part := range []struct {
		name  string
		root  xml.Name
		item  string
		check func(xml.StartElement) (bool, error)
	}{
		{"_rels/.rels", xml.Name{Space: relationshipsNS, Local: "Relationships"}, "Relationship", mainRelationship},
		{"[Content_Types].xml", xml.Name{Space: contentTypesNS, Local: "Types"}, "Override", mainContent},
	} {
		body, err := readPart(parts, part.name, maxPackageXML)
		if err != nil {
			return "", err
		}
		if err = packagePart(ctx, body, part.root, part.item, part.check); err != nil {
			return "", err
		}
	}
	body, err := readPart(parts, mainPart, maxMainBytes)
	if err != nil {
		return "", err
	}
	return bodyText(ctx, body, controls)
}

func readPart(parts map[string]*zip.File, name string, limit uint64) ([]byte, error) {
	f := parts[strings.ToLower(name)]
	if f == nil || f.Name != name || !f.Mode().IsRegular() {
		return nil, ErrInvalid
	}
	if f.UncompressedSize64 > limit {
		return nil, ErrLimit
	}
	r, err := f.Open()
	if err != nil {
		return nil, ErrInvalid
	}
	defer r.Close()
	body, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if uint64(len(body)) > limit {
		return nil, ErrLimit
	}
	if err != nil || uint64(len(body)) != f.UncompressedSize64 {
		return nil, ErrInvalid
	}
	return body, nil
}

func packagePart(ctx context.Context, body []byte, root xml.Name, item string, check func(xml.StartElement) (bool, error)) error {
	found := false
	err := walkXML(ctx, body, func(token xml.Token, depth int) error {
		e, ok := token.(xml.StartElement)
		if !ok {
			return nil
		}
		if depth == 1 && e.Name != root {
			return ErrInvalid
		}
		if depth == 2 && e.Name == (xml.Name{Space: root.Space, Local: item}) {
			match, err := check(e)
			if err != nil || match && found {
				return ErrInvalid
			}
			found = found || match
		}
		return nil
	})
	if err == nil && !found {
		return ErrInvalid
	}
	return err
}

func mainRelationship(e xml.StartElement) (bool, error) {
	switch attribute(e, "Type") {
	case "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument",
		"http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument":
	default:
		return false, nil
	}
	target, mode := attribute(e, "Target"), attribute(e, "TargetMode")
	if (target != mainPart && target != "/"+mainPart) || (mode != "" && mode != "Internal") || attribute(e, "Id") == "" {
		return false, ErrInvalid
	}
	return true, nil
}

func mainContent(e xml.StartElement) (bool, error) {
	part := attribute(e, "PartName")
	if !strings.EqualFold(part, "/"+mainPart) {
		return false, nil
	}
	if part != "/"+mainPart || attribute(e, "ContentType") != mainContentType {
		return false, ErrInvalid
	}
	return true, nil
}

func attribute(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Space == "" && a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// A strict token walk bounds XML work and rejects DTDs, custom entities,
// multiple roots and duplicate attributes. Standard XML escaping is decoded.
func walkXML(ctx context.Context, body []byte, visit func(xml.Token, int) error) error {
	d := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))))
	depth, roots := 0, 0
	for n := 0; n < maxTokens; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := d.Token()
		if err == io.EOF && depth == 0 && roots == 1 {
			return nil
		}
		if err != nil {
			return ErrInvalid
		}
		switch e := token.(type) {
		case xml.StartElement:
			depth++
			if depth > maxDepth {
				return ErrLimit
			}
			if depth == 1 {
				roots++
				if roots > 1 {
					return ErrInvalid
				}
			}
			attrs := make(map[xml.Name]bool, len(e.Attr))
			for _, a := range e.Attr {
				if attrs[a.Name] {
					return ErrInvalid
				}
				attrs[a.Name] = true
			}
		case xml.Directive:
			return ErrInvalid
		case xml.ProcInst:
			if e.Target != "xml" || roots != 0 || n != 0 {
				return ErrInvalid
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(e)) != "" {
				return ErrInvalid
			}
		}
		if err = visit(token, depth); err != nil {
			return err
		}
		if _, ok := token.(xml.EndElement); ok {
			depth--
		}
	}
	return ErrLimit
}

type node struct {
	role          string
	children      int
	contentParent int
}

func bodyText(ctx context.Context, body []byte, controls bool) (string, error) {
	var out strings.Builder
	var namespace string
	hasBody := false
	stack := []node{{}}
	write := func(s string) error {
		if len(s) > maxText-out.Len() {
			return ErrLimit
		}
		out.WriteString(s)
		return nil
	}
	err := walkXML(ctx, body, func(token xml.Token, depth int) error {
		switch e := token.(type) {
		case xml.StartElement:
			parent := len(stack) - 1
			if stack[parent].role == "sdtContent" {
				parent = stack[parent].contentParent
			}
			p := &stack[parent]
			if controls && e.Name.Space == namespace {
				switch e.Name.Local {
				case "sdt":
					switch p.role {
					case "body", "p", "tc", "tbl", "tr", "hyperlink":
						stack = append(stack, node{role: "sdt", contentParent: parent})
						return nil
					}
				case "sdtContent":
					if p.role == "sdt" {
						stack = append(stack, node{role: "sdtContent", contentParent: p.contentParent})
						return nil
					}
				}
			}
			role, separator, inline := "", "", ""
			if depth == 1 {
				namespace = e.Name.Space
				if e.Name.Local != "document" || namespace != wordNS && namespace != strictWordNS {
					return ErrInvalid
				}
				role = "document"
			} else if e.Name.Space == namespace {
				switch p.role + "/" + e.Name.Local {
				case "document/body":
					if hasBody {
						return ErrInvalid
					}
					hasBody, role = true, "body"
				case "body/p", "body/tbl", "tbl/tr", "tc/p":
					role, separator = e.Name.Local, "\n"
				case "tr/tc":
					role, separator = "tc", "\t"
				case "p/r", "p/hyperlink", "hyperlink/r", "r/t":
					role = e.Name.Local
				case "r/tab":
					inline = "\t"
				case "r/br", "r/cr":
					inline = "\n"
				}
			}
			if role != "" {
				if p.children > 0 {
					if err := write(separator); err != nil {
						return err
					}
				}
				p.children++
			}
			stack = append(stack, node{role: role})
			return write(inline)
		case xml.CharData:
			if stack[len(stack)-1].role == "t" {
				return write(string(e))
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
		return nil
	})
	if err == nil && !hasBody {
		err = ErrInvalid
	}
	if err != nil {
		return "", err
	}
	return out.String(), nil
}
