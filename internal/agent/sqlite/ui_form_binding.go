package sqlite

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

// UIFormBinding is private Host evidence, never a plugin input. The reference
// comes from the accepted projection's exact position, not matching form props.
type UIFormBinding struct {
	Component     ui.Component
	RunID         string
	GraphRevision string
	Managed       bool
}

// WebFormBinding retains historical native forms, and binds managed forms to
// their delivered Main Run. Current activation cannot reselect their policy.
func (s *Store) WebFormBinding(ctx context.Context, m conversation.Message, formID string) (UIFormBinding, error) {
	if m.Role != "assistant" {
		return UIFormBinding{}, conversation.ErrConflict
	}
	raw, err := s.WebProjection(ctx, m)
	if err != nil {
		return UIFormBinding{}, err
	}
	var spec ui.Spec
	if json.Unmarshal(raw, &spec) != nil || spec.SchemaVersion != ui.Version {
		return UIFormBinding{}, conversation.ErrConflict
	}
	bubbles, err := s.WebBubbles(ctx, m, spec)
	if err != nil {
		return UIFormBinding{}, err
	}
	// WebBubbles can discover a mismatch after visiting an earlier section. Do
	// not retain partially assigned references from a failed projection match.
	if len(bubbles) == 0 {
		if err = json.Unmarshal(raw, &spec); err != nil {
			return UIFormBinding{}, err
		}
	}
	bound, err := json.Marshal(spec)
	if err != nil {
		return UIFormBinding{}, err
	}
	c, err := ui.Form(bound, m.MessageID, formID)
	if err != nil {
		return UIFormBinding{}, err
	}
	out := UIFormBinding{Component: c}
	if c.WidgetRef == "" {
		// A current managed form cannot silently take the historical formatter
		// when its accepted projection no longer matches its bound sections.
		var migrated bool
		err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ui_widget w JOIN rsi_run b USING(run_id), json_each(b.managed_services_json) x WHERE w.message_id=? AND x.value=?)`, m.MessageID, contract.FormContract).Scan(&migrated)
		if err != nil {
			return UIFormBinding{}, err
		}
		if migrated && c.Type == "openintelligentui" {
			return UIFormBinding{}, conversation.ErrConflict
		}
		return out, nil
	}
	base, index, ok := strings.Cut(c.WidgetRef, "#")
	i, err := strconv.Atoi(index)
	if !ok || err != nil || i < 0 || strconv.Itoa(i) != index {
		return UIFormBinding{}, agent.ErrNotFound
	}
	var document, receipt string
	var managedDocument bool
	err = s.db.QueryRowContext(ctx, `SELECT `+uiStoredColumns+`,w.run_id,coalesce(b.graph_revision,''),EXISTS(SELECT 1 FROM json_each(coalesce(b.managed_services_json,'[]')) WHERE value=?)
 FROM ui_widget w JOIN run r USING(run_id) LEFT JOIN rsi_run b USING(run_id)
 JOIN conversation_intent c ON c.message_id=w.message_id JOIN tool_invocation i USING(invocation_id)
 WHERE w.ui_ref=? AND w.message_id=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND i.status='succeeded'`, contract.FormContract, base, m.MessageID).Scan(&document, &managedDocument, &receipt, &out.RunID, &out.GraphRevision, &out.Managed)
	if err != nil {
		return UIFormBinding{}, agent.ErrNotFound
	}
	widget, _, err := storedUI(document, managedDocument, receipt)
	if err != nil {
		return UIFormBinding{}, agent.ErrStorage
	}
	if i >= len(widget.Components) || widget.Components[i].Name != c.Name || ui.Hash(widget.Components[i].Props) != ui.Hash(c.Props) {
		return UIFormBinding{}, conversation.ErrConflict
	}
	if out.Managed && (!managedDocument || out.GraphRevision == "") {
		return UIFormBinding{}, agent.ErrStorage
	}
	return out, nil
}
