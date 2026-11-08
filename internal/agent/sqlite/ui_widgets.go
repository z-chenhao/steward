package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/tool"
)

func (t *tools) showUI(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	var result tool.Result
	var previewInput, previewKey, previewOrder string
	var replacedPreview string
	err := t.runs.write(ctx, func(tx *sql.Tx) error {
		kind, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if kind != "main" || name != ui.ShowName {
			result = tool.Failure("PERMISSION_DENIED")
			return nil
		}
		widget, ok := ui.ParseWidget(args)
		if !ok {
			result = tool.Failure("INVALID_ARGUMENT")
			return nil
		}
		if widget.Replace != "" {
			// Reserve an exact successful, unbound target. A second concurrent
			// correction may not branch from it; replay of this invocation may.
			var eligible int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM ui_widget w JOIN tool_invocation i USING(invocation_id) WHERE w.ui_ref=? AND w.run_id=? AND w.message_id IS NULL AND i.status='succeeded' AND w.invocation_id!=? AND NOT EXISTS (SELECT 1 FROM ui_widget n JOIN tool_invocation ni USING(invocation_id) WHERE n.run_id=w.run_id AND json_extract(n.document_json,'$.replace')=w.ui_ref AND ni.status NOT IN ('failed','cancelled') AND n.invocation_id!=?)`, widget.Replace, t.claim.RunID(), inv.ID, inv.ID).Scan(&eligible); err != nil {
				return err
			}
			if eligible != 1 {
				result = tool.Failure("NOT_FOUND")
				return nil
			}
			if err = tx.QueryRowContext(ctx, `SELECT `+uiPreviewIdentity+` FROM tool_invocation i WHERE invocation_id=?`, strings.TrimPrefix(widget.Replace, "ui:")).Scan(&replacedPreview); err != nil {
				return err
			}
		}
		// Preserve accepted arguments; all artifact consumers use the same
		// validated document, including the normalized empty script list.
		document, err := json.Marshal(widget)
		if err != nil {
			return err
		}
		var count, components, forms int
		if err = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(CASE WHEN json_array_length(document_json,'$.components')>0 THEN json_array_length(document_json,'$.components') ELSE 1 END),0),coalesce(sum((SELECT count(*) FROM json_each(w.document_json,'$.components') c WHERE json_extract(c.value,'$.type')='form' OR json_extract(c.value,'$.name') IN ('scheduleTime','steward.form'))),0) FROM ui_widget w WHERE w.run_id=? AND w.message_id IS NULL AND w.invocation_id!=? AND w.ui_ref!=? AND `+activeUIWidget, t.claim.RunID(), inv.ID, widget.Replace).Scan(&count, &components, &forms); err != nil {
			return err
		}
		added := max(1, len(widget.Components))
		for _, c := range widget.Components {
			if c.Name == "scheduleTime" || c.Name == "steward.form" {
				forms++
			}
		}
		if count >= 16 || components+added > 96 || forms > 4 {
			result = tool.Failure("RESOURCE_LIMIT")
			return nil
		}
		ref := "ui:" + inv.ID
		if _, err = tx.ExecContext(ctx, `INSERT INTO ui_widget(invocation_id,run_id,ui_ref,document_json,created_at) VALUES(?,?,?,?,?) ON CONFLICT(invocation_id) DO NOTHING`, inv.ID, t.claim.RunID(), ref, string(document), time.Now().UnixMicro()); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]string{"ui_ref": ref, "status": "staged"})
		result = tool.Result{Text: string(data)}
		previewInput = string(document)
		if err = tx.QueryRowContext(ctx, `SELECT `+uiPreviewIdentity+`,`+uiAcceptedOrder+` FROM tool_invocation i WHERE invocation_id=?`, inv.ID).Scan(&previewKey, &previewOrder); err != nil {
			return err
		}
		return nil
	})
	if err == nil && result.Error == nil {
		if replacedPreview != "" {
			ui.ObservePreview(ctx, replacedPreview, "")
		}
		ui.ObservePreview(ctx, previewKey, previewInput, previewOrder)
	}
	return result, err
}

// Supersession is derived from immutable successful receipts. No historical
// widget, accepted arguments or already-bound message is rewritten.
const activeUIWidget = `NOT EXISTS (SELECT 1 FROM ui_widget n JOIN tool_invocation ni USING(invocation_id) WHERE n.run_id=w.run_id AND json_extract(n.document_json,'$.replace')=w.ui_ref AND ni.status='succeeded')`

// Only explicit public sections opt into accepted model/parent order. Legacy
// projections retain their earlier ordering. Parallel execution timing does not
// determine a narrated answer's sequence.
const uiPreviewIdentity = `CASE WHEN i.parent_invocation_id IS NULL THEN i.provider_exchange_id||':'||i.provider_call_id ELSE i.invocation_id END`
const uiAcceptedOrder = `printf('%020d/%020d/%020d/%s',coalesce((SELECT g.step_index FROM model_generation g WHERE g.generation_id=i.provider_exchange_id),0),coalesce((SELECT p.delivery_seq FROM tool_invocation p WHERE p.invocation_id=i.parent_invocation_id),i.delivery_seq),i.created_at,i.invocation_id)`
const uiSectionOrder = `CASE WHEN EXISTS (SELECT 1 FROM ui_widget n WHERE n.run_id=w.run_id AND n.message_id IS w.message_id AND coalesce(json_extract(n.document_json,'$.text'),'')!='') THEN ` + uiAcceptedOrder + ` ELSE printf('%020d',i.delivery_seq) END`

type uiQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func appendWidgets(ctx context.Context, q uiQuerier, message string, spec ui.Spec) (ui.Spec, error) {
	rows, err := q.QueryContext(ctx, `SELECT ui_ref,document_json FROM ui_widget w JOIN tool_invocation i USING(invocation_id) WHERE w.message_id=? AND i.status='succeeded' ORDER BY `+uiSectionOrder, message)
	if err != nil {
		return spec, err
	}
	defer rows.Close()
	for rows.Next() {
		var ref, raw string
		if err = rows.Scan(&ref, &raw); err != nil {
			return spec, err
		}
		w, ok := ui.StoredWidget(raw)
		if !ok {
			return spec, agent.ErrStorage
		}
		spec.Components = append(spec.Components, ui.WidgetComponents(w, ref, message, len(spec.Components))...)
	}
	return spec, rows.Err()
}

// WebWidget reads only a delivered Main-owned artifact in the current account.
func (s *Store) WebWidget(ctx context.Context, ref string) (ui.Widget, error) {
	base, index, found := strings.Cut(ref, "#")
	if !found {
		return ui.Widget{}, agent.ErrNotFound
	}
	i, err := strconv.Atoi(index)
	if err != nil || i < 0 || strconv.Itoa(i) != index {
		return ui.Widget{}, agent.ErrNotFound
	}
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT w.document_json FROM ui_widget w JOIN run r USING(run_id) JOIN conversation_intent c ON c.message_id=w.message_id JOIN tool_invocation i USING(invocation_id) WHERE w.ui_ref=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND i.status='succeeded'`, base).Scan(&raw)
	if err != nil {
		return ui.Widget{}, agent.ErrNotFound
	}
	w, ok := ui.StoredWidget(raw)
	if !ok {
		return w, agent.ErrStorage
	}
	if i >= len(w.Components) || w.Components[i].Name != "generateSandboxedUi" {
		return ui.Widget{}, agent.ErrNotFound
	}
	w.Components = w.Components[i : i+1]
	return w, nil
}

// WebBubbles splits only a projection whose frozen parts match the bound UI
// artifacts. Stored Conversation and Delivery projections remain untouched.
// Slices share the original components, including submitted-form restoration.
func (s *Store) WebBubbles(ctx context.Context, m conversation.Message, spec ui.Spec) ([]ui.Spec, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ui_ref,document_json FROM ui_widget w JOIN tool_invocation i USING(invocation_id) WHERE w.message_id=? AND i.status='succeeded' ORDER BY `+uiSectionOrder, m.MessageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type bound struct {
		ref    string
		widget ui.Widget
	}
	var items []bound
	for rows.Next() {
		var ref, raw string
		if err := rows.Scan(&ref, &raw); err != nil {
			return nil, err
		}
		w, ok := ui.StoredWidget(raw)
		if !ok {
			return nil, agent.ErrStorage
		}
		items = append(items, bound{ref, w})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 || len(m.Content) < len(items) {
		return nil, nil
	}
	narrated := false
	for _, item := range items {
		narrated = narrated || item.widget.Text != ""
	}
	start := len(m.Content) - len(items)
	if narrated {
		start = 0
	}
	for i, item := range items {
		part := m.Content[start+i]
		if part.Type != "text" || part.Text != item.widget.Readable() {
			return nil, nil
		}
	}
	base := ui.Compose(delivery.Envelope{Message: m})
	if len(spec.Components) < len(base.Components) || ui.Hash(spec.Components[:len(base.Components)]) != ui.Hash(base.Components) {
		return nil, nil
	}
	offset := len(base.Components)
	var bubbles []ui.Spec
	for _, item := range items {
		expected := ui.WidgetComponents(item.widget, item.ref, m.MessageID, offset)
		end := offset + len(expected)
		if end > len(spec.Components) || ui.Hash(spec.Components[offset:end]) != ui.Hash(expected) {
			return nil, nil
		}
		for j := range expected {
			if expected[j].Name != "" {
				spec.Components[offset+j].WidgetRef = item.ref + "#" + strconv.Itoa(j)
			}
		}
		if item.widget.Text != "" {
			bubbles = append(bubbles, ui.Compose(delivery.Envelope{Message: conversation.Message{Content: conversation.Text(item.widget.Text)}}))
		}
		bubbles = append(bubbles, ui.Spec{SchemaVersion: ui.Version, Fallback: item.widget.Fallback, Components: spec.Components[offset:end]})
		offset = end
	}
	if offset != len(spec.Components) {
		return nil, nil
	}
	if narrated {
		m.Content = m.Content[len(items):]
	} else {
		m.Content = m.Content[:start]
	}
	reply := ui.Compose(delivery.Envelope{Message: m})
	if len(reply.Components) > 0 {
		bubbles = append(bubbles, reply)
	}
	return bubbles, nil
}

// StagedUIPreviews reads successful unbound UI artifacts under the active claim.
func (r *Runs) StagedUIPreviews(ctx context.Context, c agent.RunClaim) ([]ui.StagedPreview, error) {
	var calls []ui.StagedPreview
	err := r.write(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, c); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+uiPreviewIdentity+`,w.document_json,`+uiAcceptedOrder+` FROM ui_widget w JOIN tool_invocation i USING(invocation_id) WHERE w.run_id=? AND w.message_id IS NULL AND i.status='succeeded' AND `+activeUIWidget+` ORDER BY `+uiSectionOrder+` LIMIT 16`, c.RunID())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var call ui.StagedPreview
			if err := rows.Scan(&call.Key, &call.Arguments, &call.Order); err != nil {
				return err
			}
			calls = append(calls, call)
		}
		return rows.Err()
	})
	return calls, err
}
