package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func validCardsViews(document string, result contract.CardsViewsResult) error {
	w, err := managedUIDocument(document)
	if err != nil || result.ErrorCode != "" || result.Views == nil || len(result.Views) > 16 {
		return rsi.ErrInvalid
	}
	expected := map[int]int{}
	for i, c := range w.Components {
		if c.Name == "steward.cards" {
			var props struct {
				Items []json.RawMessage `json:"items"`
			}
			if json.Unmarshal(c.Props, &props) != nil {
				return rsi.ErrInvalid
			}
			expected[i] = len(props.Items)
		}
	}
	return validCardsViewSlots(expected, result)
}

func validCardsViewSlots(expected map[int]int, result contract.CardsViewsResult) error {
	if result.ErrorCode != "" || result.Views == nil || len(result.Views) > 16 {
		return rsi.ErrInvalid
	}
	if len(result.Views) != len(expected) {
		return rsi.ErrInvalid
	}
	for _, v := range result.Views {
		code := v.Worker
		if code == "" {
			code = v.Runtime
		}
		if (v.Worker == "") == (v.Runtime == "") {
			return rsi.ErrInvalid
		}
		if v.CompactMaxWidth < 1 || v.CompactMaxWidth > 4096 || v.CSS == "" || len(v.CSS) > 16<<10 || !utf8.ValidString(v.CSS) || strings.ContainsRune(v.CSS, 0) || expected[v.Component] < 1 || len(v.Contexts) != expected[v.Component] || v.HTML == "" || code == "" || len(v.HTML) > 512<<10 || len(code) > 16<<10 || !utf8.ValidString(v.HTML) || !utf8.ValidString(code) || strings.ContainsRune(v.HTML, 0) || strings.ContainsRune(code, 0) {
			return rsi.ErrInvalid
		}
		for _, context := range v.Contexts {
			raw, err := json.Marshal(context)
			if err != nil || context.VisibleItem == "" || !utf8.ValidString(context.Title) || !utf8.ValidString(context.VisibleItem) || utf8.RuneCountInString(context.Title) > 300 || utf8.RuneCountInString(context.VisibleItem) > 300 || !ui.ValidViewState(ui.ViewState{State: json.RawMessage(`{}`), ModelContext: raw}) {
				return rsi.ErrInvalid
			}
		}
		delete(expected, v.Component)
	}
	return nil
}

// WebCardsViewSource reads a delivered, account-owned component's accepted pure
// output. No candidate, active selection, credential or arbitrary path is read.
func (s *Store) WebCardsViewSource(ctx context.Context, ref string) (contract.CardsView, string, error) {
	base, index, ok := strings.Cut(ref, "#")
	i, err := strconv.Atoi(index)
	if !ok || err != nil || i < 0 || strconv.Itoa(i) != index || len(ref) > 256 {
		return contract.CardsView{}, "", agent.ErrNotFound
	}
	var document, receipt, raw, graph string
	var managed bool
	err = s.db.QueryRowContext(ctx, `SELECT `+uiStoredColumns+`,p.result_json,p.graph_revision FROM ui_widget w JOIN ui_cards_view_snapshot p USING(invocation_id) JOIN run r USING(run_id) JOIN rsi_run b USING(run_id) JOIN tool_invocation t USING(invocation_id) JOIN conversation_intent c ON c.message_id=w.message_id WHERE w.ui_ref=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.status='succeeded' AND p.graph_revision=b.graph_revision AND EXISTS(SELECT 1 FROM json_each(b.managed_services_json) WHERE value=?)`, base, contract.CardsViews).Scan(&document, &managed, &receipt, &raw, &graph)
	if err != nil {
		return contract.CardsView{}, "", agent.ErrNotFound
	}
	// Retain the existing accepted-normalizer/privacy boundary. Forget clears
	// that receipt; a retained view must not make revoked UI visible again.
	if _, _, err = storedUI(document, managed, receipt); err != nil {
		return contract.CardsView{}, "", agent.ErrStorage
	}
	var result contract.CardsViewsResult
	if err = rsiplugin.DecodeResult(json.RawMessage(raw), &result); err != nil {
		return contract.CardsView{}, "", agent.ErrStorage
	}
	if err = validCardsViews(document, result); err != nil {
		return contract.CardsView{}, "", agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component == i {
			return view, graph, nil
		}
	}
	return contract.CardsView{}, "", agent.ErrNotFound
}

// NULL is an explicit historical boundary. Empty managed receipts are corrupt,
// not a reason to render the current native implementation.
const uiCardsColumn = `CASE WHEN EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=w.run_id AND s.value='steward.ui-cards-view/1') THEN coalesce((SELECT p.result_json FROM ui_cards_view_snapshot p JOIN rsi_run b ON b.run_id=w.run_id WHERE p.invocation_id=w.invocation_id AND p.graph_revision=b.graph_revision),'') ELSE NULL END`

func bindCardsViewRefs(document, ref string, components []ui.Component, receipt sql.NullString) error {
	if !receipt.Valid {
		return nil
	}
	var result contract.CardsViewsResult
	if rsiplugin.DecodeResult([]byte(receipt.String), &result) != nil || validCardsViews(document, result) != nil {
		return agent.ErrStorage
	}
	for _, view := range result.Views {
		if view.Component >= len(components) {
			return agent.ErrStorage
		}
		components[view.Component].ViewRef = ref + "#" + strconv.Itoa(view.Component)
	}
	return nil
}

// WebCardsImageSource resolves only an image declared by the original,
// published Cards component. The caller never supplies an Artifact reference.
func (s *Store) WebCardsImageSource(ctx context.Context, ref string, item int) (string, string, error) {
	if item < 0 || item > 15 {
		return "", "", agent.ErrNotFound
	}
	_, graph, err := s.WebCardsViewSource(ctx, ref)
	if err != nil {
		return "", "", err
	}
	component, err := resolveUIComponent(ctx, s.db, ref)
	if err != nil || component.Name != "steward.cards" {
		return "", "", agent.ErrNotFound
	}
	var props struct {
		Items []struct {
			ImageRef string `json:"image_ref"`
		} `json:"items"`
	}
	if json.Unmarshal(component.Props, &props) != nil || item >= len(props.Items) || props.Items[item].ImageRef == "" {
		return "", "", agent.ErrNotFound
	}
	return props.Items[item].ImageRef, graph, nil
}

// WebCardsLinkSource grants only a URL in the original published component.
// Candidate markup and messages cannot supply a destination.
func (s *Store) WebCardsLinkSource(ctx context.Context, ref string, item int) (string, string, error) {
	if item < 0 || item > 15 {
		return "", "", agent.ErrNotFound
	}
	_, graph, err := s.WebCardsViewSource(ctx, ref)
	if err != nil {
		return "", "", err
	}
	component, err := resolveUIComponent(ctx, s.db, ref)
	if err != nil || component.Name != "steward.cards" {
		return "", "", agent.ErrNotFound
	}
	var props struct {
		Items []struct {
			URL string `json:"url"`
		} `json:"items"`
	}
	if json.Unmarshal(component.Props, &props) != nil || item >= len(props.Items) || props.Items[item].URL == "" {
		return "", "", agent.ErrNotFound
	}
	return props.Items[item].URL, graph, nil
}
