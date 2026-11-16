package computer

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

// BrowserSource is Host-only evidence. UTF-16LE preserves JavaScript code units,
// including isolated surrogates; none of these bytes are sent to a candidate.
type BrowserSource struct {
	Version int                `json:"version"`
	Body    []byte             `json:"body"`
	Rows    []BrowserSourceRow `json:"rows"`
}

type BrowserSourceRow struct {
	Role        []byte  `json:"role"`
	Aria        []byte  `json:"aria"`
	Inner       []byte  `json:"inner"`
	Placeholder []byte  `json:"placeholder"`
	Value       *[]byte `json:"value,omitempty"`
}

type BrowserSnapshot struct {
	Capture Capture
	Source  BrowserSource
}

// This port reads one fixed original tab; it cannot accept JS, selectors,
// endpoints or candidate-issued identities.
type BrowserSnapshotter interface {
	Snapshot(context.Context, Instance, Target, string, ObserveOptions) (BrowserSnapshot, error)
}

type BrowserRowFacts struct {
	RoleUnits, AriaUnits, InnerUnits, PlaceholderUnits int
	ValueUnits                                         *int
}
type BrowserProjectionFacts struct {
	BodyUnits int
	Rows      []BrowserRowFacts
}
type BrowserRowView struct {
	Index, LabelUnits int
	Label             string
	ValueUnits        *int
}
type BrowserProjection struct {
	BodyUnits int
	Rows      []BrowserRowView
}
type BrowserProjector func(context.Context, BrowserProjectionFacts) (BrowserProjection, error)

func (s BrowserSource) Facts() (BrowserProjectionFacts, error) {
	bounded := func(v []byte, units int) bool { return len(v)%2 == 0 && len(v)/2 <= units }
	if s.Version != 1 || !bounded(s.Body, 16384) || len(s.Rows) > 500 {
		return BrowserProjectionFacts{}, ErrInvalid
	}
	f := BrowserProjectionFacts{BodyUnits: len(s.Body) / 2, Rows: make([]BrowserRowFacts, len(s.Rows))}
	for i, r := range s.Rows {
		if !bounded(r.Role, 128<<10) || len(r.Role) == 0 || !bounded(r.Aria, 256) || !bounded(r.Inner, 256) || !bounded(r.Placeholder, 256) || r.Value != nil && !bounded(*r.Value, 4096) {
			return BrowserProjectionFacts{}, ErrInvalid
		}
		f.Rows[i] = BrowserRowFacts{RoleUnits: len(r.Role) / 2, AriaUnits: len(r.Aria) / 2, InnerUnits: len(r.Inner) / 2, PlaceholderUnits: len(r.Placeholder) / 2}
		if r.Value != nil {
			n := len(*r.Value) / 2
			f.Rows[i].ValueUnits = &n
		}
	}
	return f, nil
}

// Render proves the source, rather than selecting a second label policy. Every
// row retains its original index, role, value and native reference. A candidate
// can choose only bounded original fields, prefixes and a complete permutation.
func (s BrowserSource) Render(p BrowserProjection) (string, error) {
	f, err := s.Facts()
	if err != nil || p.BodyUnits < 0 || p.BodyUnits > f.BodyUnits || len(p.Rows) != len(s.Rows) {
		return "", ErrInvalid
	}
	var b strings.Builder
	b.WriteString(browserString(s.Body[:p.BodyUnits*2]))
	b.WriteByte('\n')
	seen := make([]bool, len(s.Rows))
	for i, choice := range p.Rows {
		if choice.Index < 0 || choice.Index >= len(s.Rows) || seen[choice.Index] {
			return "", ErrInvalid
		}
		seen[choice.Index] = true
		r := s.Rows[choice.Index]
		var label []byte
		switch choice.Label {
		case "aria":
			label = r.Aria
		case "inner":
			label = r.Inner
		case "placeholder":
			label = r.Placeholder
		case "role":
			label = r.Role
		default:
			return "", ErrInvalid
		}
		if choice.LabelUnits < 0 || choice.LabelUnits > min(len(label)/2, 256) || (r.Value == nil) != (choice.ValueUnits == nil) {
			return "", ErrInvalid
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%d] %s %s", choice.Index, browserString(r.Role), quoteBrowserText(label[:choice.LabelUnits*2]))
		if choice.ValueUnits != nil {
			if *choice.ValueUnits < 0 || *choice.ValueUnits > len(*r.Value)/2 {
				return "", ErrInvalid
			}
			b.WriteString(" = ")
			b.WriteString(quoteBrowserText((*r.Value)[:*choice.ValueUnits*2]))
		}
	}
	return b.String(), nil
}

func browserUnits(raw []byte) []uint16 {
	v := make([]uint16, len(raw)/2)
	for i := range v {
		v[i] = binary.LittleEndian.Uint16(raw[i*2:])
	}
	return v
}

func browserString(raw []byte) string { return string(utf16.Decode(browserUnits(raw))) }

// Match JSON.stringify's string spelling. In particular, a clipped/isolated
// surrogate stays an escaped code unit inside AX text, not U+FFFD or HTML data.
func quoteBrowserText(raw []byte) string {
	u := browserUnits(raw)
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(byte(c))
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < 0x20 || c >= 0xd800 && c <= 0xdfff {
				if c >= 0xd800 && c <= 0xdbff && i+1 < len(u) && u[i+1] >= 0xdc00 && u[i+1] <= 0xdfff {
					b.WriteRune(utf16.DecodeRune(rune(c), rune(u[i+1])))
					i++
				} else {
					fmt.Fprintf(&b, `\u%04x`, c)
				}
			} else {
				b.WriteRune(rune(c))
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (r UseRuntime) capture(ctx context.Context, target Target, options ObserveOptions) (Capture, error) {
	if target.Kind != "tab" || r.SnapshotBrowser == nil && r.ProjectBrowser == nil {
		// Actual pre-browser-projection Runs (through ten-service1.0.6) and
		// explicit unbundled carriers only. Remove after retained consumers and
		// remaining facade-v1 operations no longer depend on the old program.
		return r.Driver.Observe(ctx, r.Instance, target, r.Lease, options)
	}
	if r.SnapshotBrowser == nil || r.ProjectBrowser == nil {
		return Capture{}, ErrUnavailable
	}
	snapshot, err := r.SnapshotBrowser.Snapshot(ctx, r.Instance, target, r.Lease, options)
	if err != nil {
		return Capture{}, err
	}
	c := snapshot.Capture
	if c.HasAX != options.Accessibility || c.Navigation == "" {
		return Capture{}, ErrUnavailable
	}
	if !options.Accessibility {
		return c, nil
	}
	facts, err := snapshot.Source.Facts()
	if err != nil || len(c.Elements) != len(facts.Rows) {
		return Capture{}, ErrUnavailable
	}
	for i, e := range c.Elements {
		row := snapshot.Source.Rows[i]
		if e.Index != i || e.Token == "" || e.Role != browserString(row.Role) || row.Value != nil && e.Value != browserString(*row.Value) {
			return Capture{}, ErrUnavailable
		}
	}
	plan, err := r.ProjectBrowser(ctx, facts)
	if err != nil {
		return Capture{}, err
	}
	c.State, err = snapshot.Source.Render(plan)
	if err != nil {
		return Capture{}, ErrUnavailable
	}
	return c, nil
}
