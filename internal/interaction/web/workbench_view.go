package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/webworkbench/contract"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

var errWorkbenchUnavailable = errors.New("workbench presentation unavailable")

type workbenchPresentation struct {
	Presentation   contract.Presentation
	ReaderRevision string
}

func workbenchOwnedView(view string) bool {
	switch view {
	case "conversation", "tasks", "task", "execution", "attention", "schedule", "settings":
		return true
	}
	return false
}
func (s *Server) loadWorkbench(ctx context.Context, r *http.Request, p *page) error {
	if !workbenchOwnedView(p.View) || s.WebWorkbench == nil {
		return nil
	}
	reference := r.Header.Get("X-Steward-Workbench-Reader")
	if !validModelReader(reference) {
		return errWorkbenchUnavailable
	}
	out, revision, managed, err := s.WebWorkbench(ctx, reference)
	if err != nil {
		if p.View == "settings" {
			p.WorkbenchUnavailable = true
			return nil
		}
		return err
	}
	if !managed {
		return nil
	} // Explicit pre-service / unbundled embedding only.
	if revision == "" || !validModelReader(revision) || !validWorkbenchPresentation(out) {
		if p.View == "settings" {
			p.WorkbenchUnavailable = true
			return nil
		}
		return errWorkbenchUnavailable
	}
	p.Workbench = &workbenchPresentation{Presentation: out, ReaderRevision: revision}
	return nil
}
func validWorkbenchPresentation(p contract.Presentation) bool {
	for _, a := range []contract.Asset{p.View, p.Stylesheet, p.Copy} {
		if a.Source == "" || len(a.Source) > 65536 || !utf8.ValidString(a.Source) || rsiplugin.FileDigest([]byte(a.Source)) != a.Digest {
			return false
		}
	}
	var entries []contract.Translation
	if rsiplugin.DecodeInput([]byte(p.Copy.Source), &entries) != nil || len(entries) > 512 {
		return false
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Key == "" || e.English == "" || e.Chinese == "" || len(e.Key) > 8192 || len(e.English) > 8192 || len(e.Chinese) > 8192 || seen[e.Key] {
			return false
		}
		seen[e.Key] = true
	}
	policy := p.Policy
	if policy.PollMS < 200 || policy.PollMS > 60000 || policy.MaxPollMS < policy.PollMS || policy.MaxPollMS > 60000 || policy.ReadTimeoutMS < 100 || policy.ReadTimeoutMS > 10000 || len(policy.Live) > 7 || len(policy.PageLive) > 7 || len(policy.History) > 8 {
		return false
	}

	kinds := map[string]bool{}
	for _, entry := range policy.Live {
		allowed, ok := workbenchLiveSlots[entry.Kind]
		if !ok || kinds[entry.Kind] || len(entry.Slots) > len(allowed) {
			return false
		}
		kinds[entry.Kind] = true
		slots := map[string]bool{}
		for _, slot := range entry.Slots {
			if !allowed[slot] || slots[slot] {
				return false
			}
			slots[slot] = true
		}
	}
	pages := map[string]bool{}
	for _, kind := range policy.PageLive {
		if !kinds[kind] || pages[kind] {
			return false
		}
		pages[kind] = true
	}
	history := map[string]bool{}
	for _, label := range policy.History {
		if label.Kind == "" || label.Key == "" || len(label.Kind) > 64 || len(label.Key) > 512 || history[label.Kind] {
			return false
		}
		history[label.Kind] = true
	}
	return validateWorkbenchStylesheet(p.Stylesheet.Source) == nil
}
func workbenchTranslator(p contract.Presentation, language string) func(string) string {
	var entries []contract.Translation
	_ = json.Unmarshal([]byte(p.Copy.Source), &entries)
	copy := map[string]string{}
	for _, e := range entries {
		if language == "zh-CN" {
			copy[e.Key] = e.Chinese
		} else {
			copy[e.Key] = e.English
		}
	}
	return func(s string) string {
		if v, ok := copy[s]; ok {
			return v
		}
		return s
	} // Unknown source facts remain literal.
}

type workbenchBuffer struct {
	bytes.Buffer
	remaining int
}

func (w *workbenchBuffer) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, errWorkbenchUnavailable
	}
	w.remaining -= len(p)
	return w.Buffer.Write(p)
}
func (w *workbenchBuffer) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func workbenchPageLayout(p page, r *http.Request) (*template.Template, error) {
	if p.Workbench == nil || !validWorkbenchPresentation(p.Workbench.Presentation) {
		return nil, errWorkbenchUnavailable
	}
	host := requestLayout(r)
	language := requestLanguage(r)
	data, bindings := workbenchData(p, language)
	presentation := p.Workbench.Presentation
	raw, _ := json.Marshal(data)
	budget := 65536 + 16*len(raw)
	for _, value := range bindings.opaque {
		budget += len(value)
	}
	opaqueParts := map[string]template.HTML{}
	opaqueHost := func(name string, value any) (template.HTML, error) {
		key := name
		if name != "computer-panel" && name != "worker-panel" {
			raw, _ := json.Marshal(value)
			key += rsiplugin.FileDigest(raw)
		}
		if cached, ok := opaqueParts[key]; ok {
			return cached, nil
		}
		var out bytes.Buffer
		if err := host.ExecuteTemplate(&out, name, value); err != nil {
			return "", err
		}
		fragment := bindings.fragment(template.HTML(out.String()))
		opaqueParts[key] = fragment
		return fragment, nil
	}
	functions := template.FuncMap{
		"tr": workbenchTranslator(presentation, language), "canSend": canSend, "modelPickerView": modelPickerView, "obsJSON": viewJSON,
		"taskConfigurationPage": taskConfigurationPage, "taskURL": taskURL, "executionURL": executionURL, "conversationURL": conversationURL, "taskTitle": task.Title,
		"taskListURL": func(_ *workbenchTasks, field, value string) string { return taskListURL(p.Tasks, field, value) },
		"historyLabel": func(kind string) string {
			for _, entry := range presentation.Policy.History {
				if entry.Kind == kind {
					return entry.Key
				}
			}
			return kind
		},
		"inTimeZone": func(raw, zone string) string {
			at, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return raw
			}
			loc, err := time.LoadLocation(zone)
			if err != nil || zone == "" || zone == "Local" {
				return at.UTC().Format(time.RFC3339)
			}
			return at.In(loc).Format("2006-01-02 15:04:05 -07:00")
		},
		"scheduleTime": func(v *workbenchSchedule) string {
			if v == nil {
				return ""
			}
			return v.TimeLabel
		},
		"scheduleFrequency": func(v *workbenchSchedule) string {
			if v == nil {
				return ""
			}
			return v.FrequencyLabel
		},
		"identifier": identifier, "help": help, "add": func(a, b int) int { return a + b },
		"workbenchHelp":       func(v helpView) (template.HTML, error) { return opaqueHost("help", v) },
		"workbenchIdentifier": func(v identifierView) (template.HTML, error) { return opaqueHost("identifier", v) },
		"workbenchApproval": func(v workbenchApprovalRef) (template.HTML, error) {
			a, ok := bindings.approvals[v.ID]
			if !ok {
				return "", errWorkbenchUnavailable
			}
			return opaqueHost("approval-card", a)
		},
		"workbenchComputer": func() (template.HTML, error) { return opaqueHost("computer-panel", p) },
		"workbenchWorkers":  func() (template.HTML, error) { return opaqueHost("worker-panel", p) },
	}
	for name, fn := range timeFunctions(requestLocation(r), language) {
		functions[name] = fn
	}
	names := map[string]bool{}
	for _, name := range workbenchViewNames {
		names[name] = true
	}
	shared := map[string]bool{}
	for _, name := range workbenchSharedNames {
		shared[name] = true
	}
	view, err := compileInspectionView(presentation.View.Source, functions, host, inspectionTemplateSpec{root: "web-workbench-view", names: names, shared: shared, privateName: func(name string) bool { return strings.HasPrefix(name, "workbench-") }, variables: true, fields: workbenchViewFields, functions: " tr canSend modelPickerView obsJSON taskConfigurationPage taskURL executionURL conversationURL taskTitle taskListURL historyLabel inTimeZone scheduleTime scheduleFrequency identifier help add workbenchHelp workbenchIdentifier workbenchApproval workbenchComputer workbenchWorkers humanTime localTime messageDay eq ne gt and or not len printf ", unavailable: errWorkbenchUnavailable})
	if err != nil {
		return nil, err
	}
	render := func(name string) (template.HTML, error) {
		out := &workbenchBuffer{remaining: budget}
		if err := view.ExecuteTemplate(out, name, data); err != nil {
			return "", err
		}
		source := out.String()
		if !safeWorkbenchDOM(source, p) {
			return "", errWorkbenchUnavailable
		}
		for placeholder, value := range bindings.opaque {
			count := strings.Count(source, placeholder)
			if count > 1 {
				return "", errWorkbenchUnavailable
			}
			if count == 1 {
				source = strings.Replace(source, placeholder, string(value), 1)
			}
		}
		return template.HTML(source), nil
	}
	outer := requestLayout(r).Funcs(template.FuncMap{"workbenchPart": render})
	for _, name := range workbenchViewNames {
		if _, err = outer.New(name).Parse(`{{workbenchPart ` + strconv.Quote(name) + `}}`); err != nil {
			return nil, err
		}
	}
	return outer, nil
}
