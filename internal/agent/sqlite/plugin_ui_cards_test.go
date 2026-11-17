package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

func cardsViewWork(t *testing.T) (*Store, *Runs, agent.Actor) {
	t.Helper()
	raw, err := os.ReadFile("../../plugin/intelligentui/testdata/original-cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var original struct {
		Props json.RawMessage `json:"props"`
	}
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	return cardsViewWorkWithProps(t, original.Props)
}

func cardsViewWorkWithProps(t *testing.T, props json.RawMessage) (*Store, *Runs, agent.Actor) {
	t.Helper()
	s, r, a, p := childFixture(t, ui.ShowDefinition())
	installUIRuntimePlugin(t, s, r)
	input := `{"title":"Cards","fallback":"Card choices","components":[{"name":"Table","props":{"title":"Values","columns":["N"],"rows":[["42"]],"source":"Fixture"}},{"name":"steward.cards","props":` + string(props) + `}]}`
	var step atomic.Int32
	p.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		if step.Add(1) == 1 {
			return calls(ui.ShowName, input), nil
		}
		return llm.GenerationResult{Text: "Cards ready."}, nil
	}
	mustSubmit(t, a, "cards-receipt-recovery", "Present original cards")
	return s, r, a
}

func TestUICardsViewSnapshotSurvivesReceiptFailureWithoutCandidateReplay(t *testing.T) {
	s, r, a := cardsViewWork(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_cards_receipt BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND NEW.tool_name='builtin__intelligent_ui_show' BEGIN SELECT RAISE(ABORT,'cards receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("receipt fault not exercised", err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, s, `SELECT count(*) FROM ui_cards_view_snapshot`) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-cards%'`) != 0 {
		t.Fatal("pure view was reserved as an effect or lost its artifact")
	}
	var ref, saved string
	if err := s.db.QueryRow(`SELECT w.ui_ref,p.result_json FROM ui_widget w JOIN ui_cards_view_snapshot p USING(invocation_id)`).Scan(&ref, &saved); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WebCardsViewSource(ctx, ref+"#1"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal("unpublished cards exposed", err)
	}
	r.plugins = nil // Loss of candidate execution must not alter accepted staging.
	if _, err := s.db.Exec(`DROP TRIGGER fail_cards_receipt`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	view, graph, err := s.WebCardsViewSource(ctx, ref+"#1")
	if err != nil || graph == "" || !strings.Contains(view.HTML, "Card choices") || !strings.Contains(view.HTML, "Ship safe core") || view.Worker == "" || view.Runtime != "" {
		t.Fatal("original view did not recover", view, graph, err)
	}
	var messageRaw string
	if err = s.db.QueryRow(`SELECT c.message_json FROM ui_widget w JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=?`, ref).Scan(&messageRaw); err != nil {
		t.Fatal(err)
	}
	var message conversation.Message
	if err = json.Unmarshal([]byte(messageRaw), &message); err != nil {
		t.Fatal(err)
	}
	projected, err := s.WebProjection(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	var spec ui.Spec
	if err = json.Unmarshal(projected, &spec); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range spec.Components {
		if c.Name == "steward.cards" {
			found = true
			if c.ViewRef != ref+"#1" {
				t.Fatal("view not bound to original position", c)
			}
		}
	}
	if !found {
		t.Fatal("managed cards missing from actual projection")
	}
	bubbles, err := s.WebBubbles(ctx, message, spec)
	if err != nil || len(bubbles) == 0 {
		t.Fatal("view metadata changed frozen projection identity", bubbles, err)
	}
	var after string
	if err = s.db.QueryRow(`SELECT result_json FROM ui_cards_view_snapshot`).Scan(&after); err != nil || saved != after {
		t.Fatal("recovery changed frozen view", err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, s, `SELECT count(*) FROM ui_cards_view_snapshot`) != 1 {
		t.Fatal("recovery duplicated view")
	}
	if _, _, err = s.WebCardsViewSource(ctx, ref+"#2"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal("unbound index exposed", err)
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); err != nil {
			return err
		}
		return r.revokeMemory(ctx, tx, owner)
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.WebCardsViewSource(ctx, ref+"#1"); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("retained view bypassed actual Forget", err)
	}

}

func TestUICardsViewSnapshotRollbackDoesNotReserveEffect(t *testing.T) {
	s, _, a := cardsViewWork(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_cards_snapshot BEFORE INSERT ON ui_cards_view_snapshot BEGIN SELECT RAISE(ABORT,'cards snapshot fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("snapshot failure not exercised", err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, s, `SELECT count(*) FROM ui_cards_view_snapshot`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call WHERE operation_id LIKE 'ui-cards%'`) != 0 {
		t.Fatal("failed pure computation reserved an artifact or effect")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_cards_snapshot`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	var ref string
	if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if view, _, err := s.WebCardsViewSource(ctx, ref+"#1"); err != nil || !strings.Contains(view.HTML, "Ship safe core") {
		t.Fatal("original input did not recover after rollback", view, err)
	}
	if scalar(t, s, `SELECT count(*) FROM ui_widget`) != 1 || scalar(t, s, `SELECT count(*) FROM ui_cards_view_snapshot`) != 1 {
		t.Fatal("recovery duplicated accepted view")
	}
}

func TestUICardsViewRefBindingRejectsMissingManagedReceipt(t *testing.T) {
	document := `{"title":"Cards","fallback":"Cards","components":[{"name":"steward.cards","props":{"items":[{"title":"A"}]}}]}`
	components := []ui.Component{{Name: "steward.cards"}}
	if err := bindCardsViewRefs(document, "ui:original", components, sql.NullString{Valid: true}); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("managed view silently fell back", err)
	}
	if err := bindCardsViewRefs(document, "ui:original", components, sql.NullString{}); err != nil || components[0].ViewRef != "" {
		t.Fatal("historical view was reinterpreted", components, err)
	}
}

func TestUICardsViewRejectsMissingDuplicateAndOtherComponentSlots(t *testing.T) {
	document := `{"title":"Cards","fallback":"Cards","components":[{"name":"steward.cards","props":{"items":[{"title":"A"}]}},{"name":"Table","props":{"title":"Values","columns":["N"],"rows":[],"source":"Fixture"}},{"name":"steward.cards","props":{"items":[{"title":"B"}]}}]}`
	for _, result := range []contract.CardsViewsResult{
		{},
		{Views: []contract.CardsView{}},
		{Views: []contract.CardsView{{Component: 0, HTML: "A", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}}},
		{Views: []contract.CardsView{{Component: 0, HTML: "A", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}, {Component: 0, HTML: "B", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}}},
		{Views: []contract.CardsView{{Component: 0, HTML: "A", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}, {Component: 1, HTML: "B", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}}},
		{Views: []contract.CardsView{{Component: 0, HTML: "A", Runtime: "", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}, {Component: 2, HTML: "B", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}}},
		{Views: []contract.CardsView{{Component: 0, HTML: "A", Runtime: strings.Repeat("x", (16<<10)+1), CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}, {Component: 2, HTML: "B", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}}},
		{Views: []contract.CardsView{{Component: 0, HTML: "A\x00", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}, {Component: 2, HTML: "B", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}}},
	} {
		if validCardsViews(document, result) == nil {
			t.Fatal("invalid or incomplete Cards slots accepted", result)
		}
	}
	valid := contract.CardsViewsResult{Views: []contract.CardsView{{Component: 0, HTML: "A", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}, {Component: 2, HTML: "B", Runtime: "void 0", CSS: "p{}", CompactMaxWidth: 760, Contexts: []contract.CardsContext{{VisibleItem: "A"}}}}}
	if err := validCardsViews(document, valid); err != nil {
		t.Fatal("complete original Cards slots rejected", err)
	}
	for _, contexts := range [][]contract.CardsContext{nil, {}, {{VisibleItem: "A"}, {VisibleItem: "B"}}, {{VisibleItem: ""}}, {{VisibleItem: strings.Repeat("x", 301)}}, {{Title: "bad\x00", VisibleItem: "A"}}} {
		result := valid
		result.Views = append([]contract.CardsView(nil), valid.Views...)
		result.Views[0].Contexts = contexts
		if validCardsViews(document, result) == nil {
			t.Fatal("missing or malformed frozen context accepted", contexts)
		}
	}
}

func TestUICardsImageGrantUsesPublishedOriginalItemAndPrivacyFence(t *testing.T) {
	s, r, a := cardsViewWorkWithProps(t, json.RawMessage(`{"title":"Images","items":[{"title":"Alpha","image_ref":"artifact:declared_image"},{"title":"Beta"}]}`))
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_cards_image_receipt BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND NEW.tool_name='builtin__intelligent_ui_show' BEGIN SELECT RAISE(ABORT,'image receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("receipt failure not exercised", err)
	}
	var ref string
	if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WebCardsImageSource(ctx, ref+"#1", 0); err == nil {
		t.Fatal("unpublished image grant exposed")
	}
	r.plugins = nil
	if _, err := s.db.Exec(`DROP TRIGGER fail_cards_image_receipt`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	resource, graph, err := s.WebCardsImageSource(ctx, ref+"#1", 0)
	if err != nil || resource != "artifact:declared_image" || graph == "" {
		t.Fatal("original image grant not retained", resource, graph, err)
	}
	for _, bad := range []struct {
		ref  string
		item int
	}{{ref + "#0", 0}, {ref + "#2", 0}, {ref + "#01", 0}, {ref + "#1", -1}, {ref + "#1", 1}, {ref + "#1", 2}, {ref + "#1", 16}} {
		if _, _, err := s.WebCardsImageSource(ctx, bad.ref, bad.item); err == nil {
			t.Fatal("another component or undeclared image granted", bad)
		}
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); err != nil {
			return err
		}
		return r.revokeMemory(ctx, tx, owner)
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.WebCardsImageSource(ctx, ref+"#1", 0); err == nil {
		t.Fatal("Forget did not revoke original image grant")
	}
}

func TestUICardsLinkGrantUsesPublishedOriginalItemAndPrivacyFence(t *testing.T) {
	s, r, a := cardsViewWorkWithProps(t, json.RawMessage(`{"title":"Links","items":[{"title":"Alpha","url":"https://example.org/original?x=1"},{"title":"Beta"}]}`))
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_cards_link_receipt BEFORE UPDATE ON tool_invocation WHEN NEW.status='succeeded' AND NEW.tool_name='builtin__intelligent_ui_show' BEGIN SELECT RAISE(ABORT,'link receipt fault'); END`); err != nil {
		t.Fatal(err)
	}
	if err := a.Drain(ctx); !errors.Is(err, agent.ErrStorage) {
		t.Fatal("receipt failure not exercised", err)
	}
	var ref string
	if err := s.db.QueryRow(`SELECT ui_ref FROM ui_widget`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WebCardsLinkSource(ctx, ref+"#1", 0); err == nil {
		t.Fatal("unpublished link grant exposed")
	}
	r.plugins = nil
	if _, err := s.db.Exec(`DROP TRIGGER fail_cards_link_receipt`); err != nil {
		t.Fatal(err)
	}
	mustDrain(t, a)
	resource, graph, err := s.WebCardsLinkSource(ctx, ref+"#1", 0)
	if err != nil || resource != "https://example.org/original?x=1" || graph == "" {
		t.Fatal("original link grant not retained", resource, graph, err)
	}
	for _, bad := range []struct {
		ref  string
		item int
	}{{ref + "#0", 0}, {ref + "#2", 0}, {ref + "#01", 0}, {ref + "#1", -1}, {ref + "#1", 1}, {ref + "#1", 2}, {ref + "#1", 16}} {
		if _, _, err := s.WebCardsLinkSource(ctx, bad.ref, bad.item); err == nil {
			t.Fatal("another component or undeclared link granted", bad)
		}
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); err != nil {
			return err
		}
		return r.revokeMemory(ctx, tx, owner)
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.WebCardsLinkSource(ctx, ref+"#1", 0); err == nil {
		t.Fatal("Forget did not revoke original link grant")
	}
}

func TestUICardsPreviewUsesOriginalPureServiceWithoutDeliveryGrants(t *testing.T) {
	ctx := context.Background()
	s, r, a, _ := childFixture(t, ui.ShowDefinition())
	installUIRuntimePlugin(t, s, r)
	receipt := mustSubmit(t, a, "cards-preview", "Show cards")
	c, err := r.Claim(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, c)
	// The outer JSON is still arriving; only a complete card is previewed.
	raw := `{"title":"Draft","components":[{"name":"Table","props":{"title":"Values","columns":["N"],"rows":[["42"]],"source":"Fixture"}},{"name":"steward.cards","props":{"title":"Choices","items":[{"title":"Alpha","text":"Original card","image_ref":"artifact:unpublished","url":"https://example.org/original"}]`
	preview, valid, err := r.UIPreview(ctx, c, raw)
	view := preview.CardsViews[1]
	if err != nil || !valid || !preview.CardsManaged || len(preview.CardsViews) != 1 || !strings.Contains(view.HTML, "Original card") || view.CompactMaxWidth != 760 || view.CSS == "" {
		t.Fatal("original pure preview missing", preview, valid, err)
	}
	if view.Runtime != "" || view.Worker != "" || len(view.Contexts) != 0 || strings.Contains(view.HTML, "data-card-image") || strings.Contains(view.HTML, "artifact:unpublished") {
		t.Fatal("draft acquired delivery grants", view)
	}
	wire, _ := json.Marshal(preview)
	if !strings.Contains(string(wire), `"cards_view":true`) || strings.Contains(string(wire), "<article") || strings.Contains(string(wire), "native-result-card") {
		t.Fatal("private view leaked into event wire", string(wire))
	}
	if scalar(t, s, `SELECT count(*) FROM ui_cards_view_snapshot`) != 0 || scalar(t, s, `SELECT count(*) FROM ui_widget`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("preview created durable delivery or effect")
	}
	if err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var owner string
		if e := tx.QueryRow(`SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&owner); e != nil {
			return e
		}
		return r.revokeMemory(ctx, tx, owner)
	}); err != nil {
		t.Fatal(err)
	}
	if _, valid, err = r.UIPreview(ctx, c, raw); err == nil || valid {
		t.Fatal("Forget did not revoke preview", err)
	}
}
