package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// InspectMemoryPresented freezes one owner-read graph before reading facts. The
// candidate sees route values, not the page, bodies, source text or store. Its
// execution precedes the original snapshot/privacy checks on every read branch.
func (r *Runs) InspectMemoryPresented(ctx context.Context, q memory.InspectionQuery, store *memorysqlite.Store, reference string) (memory.InspectionPage, *memory.InspectionPresentation, error) {
	revision, managed, err := r.host.ownerReadServiceSelection(ctx, contract.Presentation)
	if err != nil {
		return memory.InspectionPage{}, nil, err
	}
	if reference != "" {
		revision, managed = reference, true
	}
	if !managed {
		p, e := r.InspectMemory(ctx, q, store)
		return p, nil, e
	}
	if r.plugins == nil {
		return memory.InspectionPage{}, nil, memory.ErrInspectionUnavailable
	}
	var presentation *memory.InspectionPresentation
	page, err := r.inspectMemory(ctx, q, store, func(c context.Context, a evidenceAccess, page memory.InspectionPage) error {
		req := memoryInspectionNavigation(q, page)
		var e error
		presentation, e = r.memoryInspectorPresentation(c, a, revision, req)
		return e
	})
	if err != nil {
		return memory.InspectionPage{}, nil, err
	}
	return page, presentation, nil
}

func memoryInspectionNavigation(q memory.InspectionQuery, page memory.InspectionPage) contract.Request {
	query := contract.Query{Layer: q.Layer, SourcesOffset: q.SourcesOffset, Since: q.Since, Until: q.Until, Ref: q.Ref, Search: q.Search, Tier: q.Tier, Origin: q.Origin, Status: q.Status, View: q.View, Cursor: q.Cursor, Limit: q.Limit, Source: q.Source, Offset: q.Offset}
	req := contract.Request{Navigation: []contract.Navigation{}}
	add := func(key, value string) {
		for _, n := range req.Navigation {
			if n.Key == key && n.Value == value {
				return
			}
		}
		req.Navigation = append(req.Navigation, contract.Navigation{Query: query, Key: key, Value: value})
	}
	add("ref", "")
	add("cursor", "")
	add("view", "maintenance")
	for _, layer := range []string{"transcripts", "notes", "representations", "injected"} {
		add("layer", layer)
	}
	if page.Next != "" {
		add("cursor", page.Next)
	}
	for _, m := range page.Maintenance {
		add("ref", m.Ref)
		for _, change := range m.Changes {
			add("ref", change.Ref)
		}
	}
	for _, item := range page.Items {
		add("ref", item.Ref)
		if q.Ref == "" {
			continue
		}
		for _, related := range item.Related {
			if related.Ref != "" {
				add("ref", related.Ref)
			}
		}
		if item.Supersedes != "" {
			add("ref", item.Supersedes)
		}
		if item.ReplacedBy != "" {
			add("ref", item.ReplacedBy)
		}
		if item.NextSourcesOffset != nil {
			add("sources_after", strconv.Itoa(*item.NextSourcesOffset))
		}
		for _, source := range item.Sources {
			if source.Kind == "conversation" || source.Kind == "task" {
				add("source", source.Ref)
			}
			if q.Source == source.Ref && source.NextOffset != nil {
				add("offset", strconv.FormatInt(*source.NextOffset, 10))
			}
		}
	}
	return req
}

type memoryInspectorDigests struct{ View, Styles, Copy, Program string }

func acceptMemoryInspectionPresentation(revision string, req contract.Request, result contract.Result, digests memoryInspectorDigests) (*memory.InspectionPresentation, error) {
	invalid := memory.ErrInspectionUnavailable
	digest, stylesDigest := digests.View, digests.Styles
	if digests.Program == "" || result.Program.Resource != contract.ProgramResource || result.Program.Digest != digests.Program || rsiplugin.FileDigest([]byte(result.Program.Source)) != digests.Program || len(result.Program.Source) == 0 || len(result.Program.Source) > 64<<10 || !utf8.ValidString(result.Program.Source) {
		return nil, invalid
	}
	if digests.Copy == "" || result.Localization.Resource != contract.LocalizationResource || result.Localization.Digest != digests.Copy || rsiplugin.FileDigest([]byte(result.Localization.Source)) != digests.Copy {
		return nil, invalid
	}
	var translations []contract.Translation
	if err := rsiplugin.Decode([]byte(result.Localization.Source), &translations); err != nil || len(translations) == 0 || len(translations) > 256 {
		return nil, invalid
	}
	copy := make([]memory.InspectionTranslation, len(translations))
	seen := map[string]bool{}
	for i, tr := range translations {
		if tr.English == "" || tr.Chinese == "" || len(tr.English) > 1024 || len(tr.Chinese) > 2048 || seen[tr.English] {
			return nil, invalid
		}
		seen[tr.English] = true
		copy[i] = memory.InspectionTranslation{English: tr.English, Chinese: tr.Chinese}
	}
	if digest == "" || result.View.Resource != contract.ViewResource || result.View.Digest != digest || rsiplugin.FileDigest([]byte(result.View.Source)) != digest {
		return nil, invalid
	}
	if revision == "" || len(result.URLs) != len(req.Navigation) {
		return nil, invalid
	}
	if stylesDigest == "" || result.Stylesheet.Resource != contract.StylesheetResource || result.Stylesheet.Digest != stylesDigest || rsiplugin.FileDigest([]byte(result.Stylesheet.Source)) != stylesDigest {
		return nil, invalid
	}
	s, o, l := result.Labels.Source, result.Labels.Origin, result.Labels.Layer
	for _, text := range []string{s.Conversation, s.Task, s.Tool, s.Control, s.Other, o.Written, o.Dream, o.Original, o.Automatic, o.Explicit, o.Other, l.Transcripts, l.Representations, l.Injected, l.Notes, result.Labels.DefaultStatus} {
		if text == "" || len(text) > 300 {
			return nil, invalid
		}
	}
	p := &memory.InspectionPresentation{ReaderRevision: revision, Program: result.Program.Source, ProgramDigest: digests.Program, Copy: copy, CopyDigest: digests.Copy, ViewSource: result.View.Source, ViewDigest: digest, Stylesheet: result.Stylesheet.Source, StylesheetDigest: stylesDigest, Links: make([]memory.InspectionLink, len(result.URLs)), Source: memory.InspectionSourceLabels{Conversation: s.Conversation, Task: s.Task, Tool: s.Tool, Control: s.Control, Other: s.Other}, Origin: memory.InspectionOriginLabels{Written: o.Written, Dream: o.Dream, Original: o.Original, Automatic: o.Automatic, Explicit: o.Explicit, Other: o.Other}, Layer: memory.InspectionLayerLabels{Transcripts: l.Transcripts, Representations: l.Representations, Injected: l.Injected, Notes: l.Notes}, DefaultStatus: result.Labels.DefaultStatus}
	for i, raw := range result.URLs {
		u, e := url.Parse(raw)
		if e != nil || len(raw) > 16384 || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.Path != "/developer/memory" || u.RawPath != "" {
			return nil, invalid
		}
		values, e := url.ParseQuery(u.RawQuery)
		if e != nil || values.Encode() != u.RawQuery {
			return nil, invalid
		}
		for key, items := range values {
			if len(items) != 1 {
				return nil, invalid
			}
			switch key {
			case "layer", "ref", "search", "tier", "origin", "status", "view", "cursor", "source", "offset", "sources_after", "since", "until", "limit":
			default:
				return nil, invalid
			}
		}
		n := req.Navigation[i]
		// Candidate may compose/reset navigation; it cannot invent another identity.
		allowedRef := n.Query.Ref
		if n.Key == "ref" {
			allowedRef = n.Value
			if n.Query.View == "maintenance" {
				allowedRef = strings.Replace(allowedRef, "memory-event:", "memory:maintenance-", 1)
			}
		}
		if n.Key == "ref" && n.Value != "" && values.Get("ref") != allowedRef {
			return nil, invalid
		}
		if ref := values.Get("ref"); ref != "" && ref != allowedRef {
			return nil, invalid
		}
		allowedSource := n.Query.Source
		if n.Key == "source" {
			allowedSource = n.Value
		}
		if n.Key == "source" && n.Value != "" && values.Get("source") != allowedSource {
			return nil, invalid
		}
		if source := values.Get("source"); source != "" && source != allowedSource {
			return nil, invalid
		}
		values.Set("inspector_revision", revision)
		u.RawQuery = values.Encode()
		p.Links[i] = memory.InspectionLink{Key: n.Key, Value: n.Value, URL: u.String()}
	}
	return p, nil
}

func memoryInspectionResourceDigest(ctx context.Context, h rsi.Host, revision, name, path string) (string, error) {
	b, err := h.ServiceInstance(ctx, revision, contract.Presentation)
	if err != nil {
		return "", err
	}
	p, err := h.Catalog.Load(b.PackageDigest)
	if err != nil {
		return "", err
	}
	count := 0
	for _, resource := range p.Manifest.Resources {
		if resource.Kind == "view" && resource.Name == name {
			if resource.Path != path {
				return "", memory.ErrInspectionUnavailable
			}
			count++
		}
	}
	if count != 1 {
		return "", memory.ErrInspectionUnavailable
	}
	for _, file := range p.Manifest.Files {
		if file.Path == path {
			return file.Digest, nil
		}
	}
	return "", memory.ErrInspectionUnavailable
}

func (r *Runs) memoryInspectorPresentation(c context.Context, a evidenceAccess, revision string, req contract.Request) (*memory.InspectionPresentation, error) {
	if r.plugins == nil {
		return nil, memory.ErrInspectionUnavailable
	}
	input, e := json.Marshal(req)
	if e != nil {
		return nil, e
	}
	host := *r.plugins
	scoped := r.host.Plugins()
	scoped.Fence = func(ctx context.Context, tx *sql.Tx) error { return r.evidenceFence(ctx, tx, a) }
	host.Store = scoped
	raw, e := host.ReadService(c, revision, contract.Presentation, input, nil)
	if e != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	var result contract.Result
	if e = rsiplugin.DecodeResult(raw, &result); e != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	view, e := memoryInspectionResourceDigest(c, host, revision, contract.ViewResource, contract.ViewFile)
	if e != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	styles, e := memoryInspectionResourceDigest(c, host, revision, contract.StylesheetResource, contract.StylesheetFile)
	if e != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	localization, e := memoryInspectionResourceDigest(c, host, revision, contract.LocalizationResource, contract.LocalizationFile)
	if e != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	program, e := memoryInspectionResourceDigest(c, host, revision, contract.ProgramResource, contract.ProgramFile)
	if e != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	p, e := acceptMemoryInspectionPresentation(revision, req, result, memoryInspectorDigests{View: view, Styles: styles, Copy: localization, Program: program})
	if e != nil {
		return nil, e
	}
	if e = host.Store.CheckGraph(c, revision); e != nil {
		return nil, memory.ErrInspectionUnavailable
	}
	return p, nil
}

// This asset read has no Memory store/query parameter and never loads documents.
func (r *Runs) MemoryInspectorStylesheet(ctx context.Context, reference string) (memory.InspectionStylesheet, error) {
	if reference == "" {
		return memory.InspectionStylesheet{}, memory.ErrInspectionInvalid
	}
	var access evidenceAccess
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error { var e error; access, e = r.evidenceAccess(ctx, tx, nil); return e })
	if err != nil {
		return memory.InspectionStylesheet{}, memory.ErrInspectionUnavailable
	}
	p, err := r.memoryInspectorPresentation(ctx, access, reference, contract.Request{Navigation: []contract.Navigation{}})
	if err != nil {
		return memory.InspectionStylesheet{}, err
	}
	return memory.InspectionStylesheet{ReaderRevision: reference, Source: p.Stylesheet}, nil
}

func (r *Runs) MemoryInspectorProgram(ctx context.Context, reference string) (memory.InspectionProgram, error) {
	if reference == "" {
		return memory.InspectionProgram{}, memory.ErrInspectionInvalid
	}
	var access evidenceAccess
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error { var e error; access, e = r.evidenceAccess(ctx, tx, nil); return e })
	if err != nil {
		return memory.InspectionProgram{}, memory.ErrInspectionUnavailable
	}
	p, err := r.memoryInspectorPresentation(ctx, access, reference, contract.Request{Navigation: []contract.Navigation{}})
	if err != nil {
		return memory.InspectionProgram{}, err
	}
	return memory.InspectionProgram{ReaderRevision: reference, Source: p.Program}, nil
}
