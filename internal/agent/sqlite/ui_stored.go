package sqlite

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Every consumer uses the immutable Run boundary. A managed artifact requires
// the accepted normalizer receipt from that graph's exact service instance.
// Missing receipts never select the historical native interpreter.
const uiStoredColumns = `w.document_json,
 EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=w.run_id AND s.value='steward.ui-document/1'),
 coalesce((SELECT CASE WHEN c.result_json IS NOT NULL THEN json_object('version',p.version,'output',json(c.result_json)) END FROM rsi_run r JOIN rsi_graph g ON g.revision=r.graph_revision,
 json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),
 json_each(p.manifest_json,'$.provides') s JOIN rsi_call c ON c.run_id=r.run_id AND c.instance_id=json_extract(b.value,'$.instance_id') AND c.operation_id='ui-normalize:'||w.invocation_id
 WHERE r.run_id=w.run_id AND json_extract(s.value,'$.contract')='steward.ui-document/1'),'')`

func storedUI(raw string, managed bool, receipt string) (ui.Widget, string, error) {
	if !managed {
		w, ok := ui.StoredWidget(raw)
		if !ok {
			return w, "", agent.ErrStorage
		}
		return w, w.Readable(), nil
	}
	var envelope struct {
		Version string          `json:"version"`
		Output  json.RawMessage `json:"output"`
	}
	if rsiplugin.DecodeResult([]byte(receipt), &envelope) != nil {
		return ui.Widget{}, "", agent.ErrStorage
	}
	return storedUIAtVersion(raw, true, string(envelope.Output), envelope.Version)
}

func storedUIAtVersion(raw string, managed bool, receipt, version string) (ui.Widget, string, error) {
	if !managed {
		return storedUI(raw, false, "")
	}
	var result contract.Result
	if rsiplugin.DecodeResult([]byte(receipt), &result) != nil || result.ErrorCode != "" {
		return ui.Widget{}, "", agent.ErrStorage
	}
	w, err := managedUIDocument(raw)
	if err != nil {
		return ui.Widget{}, "", err
	}
	accepted, err := managedUIDocument(result.DocumentJSON)
	if err != nil || ui.Hash(w) != ui.Hash(accepted) {
		return ui.Widget{}, "", agent.ErrStorage
	}
	switch version {
	case "1.0.0", "1.1.0", "1.1.1", "1.2.0", "1.3.0", "1.4.0":
		// These exact historical generations accepted documents before component
		// summaries belonged to the normalizer. Never select this path by absence.
		legacy := ui.WidgetComponents(w, "", "", 0)
		for i := range w.Components {
			w.Components[i].Text = legacy[i].Text
		}
	default:
		if len(result.ComponentText) != len(w.Components) {
			return ui.Widget{}, "", agent.ErrStorage
		}
		for i := range w.Components {
			w.Components[i].Text = result.ComponentText[i]
		}
	}
	if err := bindAcceptedMeetingViews(&w, result.MeetingViews, version); err != nil {
		return ui.Widget{}, "", err
	}
	if err := bindAcceptedActionViews(&w, result.ActionViews, version); err != nil {
		return ui.Widget{}, "", err
	}
	return w, result.Readable, nil
}

// The presentation wire shape excludes Host component fields. A plugin cannot
// manufacture approval cards, delivery identities, accepted forms or statuses
// by returning an internal delivery.Component object as its normalized output.
func managedUIDocument(raw string) (ui.Widget, error) {
	var wire struct {
		Title      string `json:"title"`
		Text       string `json:"text,omitempty"`
		Replace    string `json:"replace,omitempty"`
		Fallback   string `json:"fallback"`
		Components []struct {
			Name  string          `json:"name"`
			Props json.RawMessage `json:"props"`
		} `json:"components"`
	}
	if rsiplugin.DecodeResult([]byte(raw), &wire) != nil || len(wire.Components) == 0 || len(wire.Components) > 16 {
		return ui.Widget{}, agent.ErrStorage
	}
	w := ui.Widget{Title: wire.Title, Text: wire.Text, Replace: wire.Replace, Fallback: wire.Fallback, Components: make([]ui.Component, len(wire.Components))}
	for i, c := range wire.Components {
		if c.Name == "" {
			return ui.Widget{}, agent.ErrStorage
		}
		w.Components[i] = ui.Component{Name: c.Name, Props: c.Props}
	}
	return w, nil
}
