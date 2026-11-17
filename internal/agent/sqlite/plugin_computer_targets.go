package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (t *tools) computerInventoryOrderer(ctx context.Context, inv tool.Invocation) (computer.InventoryOrderer, error) {
	host, revision, err := t.computerContentHost(ctx, inv, contract.InventoryOrderContract)
	if err != nil || host == nil {
		return nil, err
	}
	return func(ctx context.Context, kind string, values []computer.Target) ([]int, error) {
		labels := make([]contract.TargetLabel, len(values))
		for i, v := range values {
			labels[i] = contract.TargetLabel{Name: v.Name, URL: v.URL}
		}
		facts, _ := json.Marshal(contract.InventoryFacts{Kind: kind})
		data, _ := json.Marshal(contract.InventoryLabels{Labels: labels})
		raw, err := host.ReadService(ctx, revision, contract.InventoryOrderContract, facts, []rsi.InputData{{ID: "targets", Data: data}})
		if err != nil {
			return nil, err
		}
		var order contract.InventoryOrder
		if rsiplugin.DecodeResult(raw, &order) != nil {
			return nil, rsi.ErrInvalid
		}
		return order.Indices, nil
	}, nil
}

// Exact equality and Unicode EqualFold need at most four source bytes per
// selector byte. Longer irrelevant labels remain explicitly incomplete, so a
// 12 MiB Driver inventory never introduces a new 8 MiB binding-input ceiling.
// Labels, not raw identities/Native data, are the only per-record disclosure.
func computerMatchText(value string, limit int) contract.CompleteText {
	if len(value) <= limit {
		return contract.CompleteText{Text: value, Complete: true}
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return contract.CompleteText{Text: value[:end], Complete: false}
}

func (t *tools) computerTargetMatcher(ctx context.Context, inv tool.Invocation) (computer.TargetMatcher, error) {
	host, revision, err := t.computerContentHost(ctx, inv, contract.TargetMatchContract)
	if err != nil || host == nil {
		return nil, err
	}
	return func(ctx context.Context, kind, selector string, values []computer.Target) ([]int, error) {
		facts, _ := json.Marshal(contract.MatchFacts{Kind: kind})
		matches := []int{}
		limit := max(256, 4*len(selector))
		for start := 0; start < len(values); {
			labels := make([]contract.MatchLabel, 0, min(512, len(values)-start))
			size := 0
			for index := start; index < len(values) && len(labels) < 512; index++ {
				v := values[index]
				if !utf8.ValidString(v.Name) || !utf8.ValidString(v.URL) {
					return nil, rsi.ErrInvalid
				}
				label := contract.MatchLabel{Name: computerMatchText(v.Name, limit), URL: computerMatchText(v.URL, limit), IDMatch: selector == v.ID}
				encoded, _ := json.Marshal(label)
				if size+len(encoded) > 512<<10 && len(labels) > 0 {
					break
				}
				labels = append(labels, label)
				size += len(encoded)
			}
			data, _ := json.Marshal(contract.MatchLabels{Selector: selector, Labels: labels})
			raw, err := host.ReadService(ctx, revision, contract.TargetMatchContract, facts, []rsi.InputData{{ID: "targets", Data: data}})
			if err != nil {
				return nil, err
			}
			var selected contract.TargetMatches
			if rsiplugin.DecodeResult(raw, &selected) != nil {
				return nil, rsi.ErrInvalid
			}
			seen := make([]bool, len(labels))
			for _, index := range selected.Indices {
				if index < 0 || index >= len(labels) || seen[index] {
					return nil, rsi.ErrInvalid
				}
				seen[index] = true
				matches = append(matches, start+index)
			}
			// Matches across batches are never collapsed into an arbitrary first hit.
			// Once ambiguous, releasing further labels cannot restore uniqueness.
			if len(matches) > 1 {
				return matches, nil
			}
			start += len(labels)
		}
		return matches, nil
	}, nil
}

func (t *tools) commitComputerTargets(ctx context.Context, tx *sql.Tx, receipt computer.TargetReceipt) error {
	if err := t.commitComputerDocumentation(ctx, tx, computer.DocumentationReceipt{Lease: receipt.Lease, Instance: receipt.Instance}); err != nil {
		return err
	}
	if receipt.Token == "" {
		return nil
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT state_json FROM computer_use WHERE execution_id=?`, receipt.Lease).Scan(&raw); err != nil {
		return err
	}
	var state computer.UseState
	if json.Unmarshal([]byte(raw), &state) != nil {
		return agent.ErrClaimLost
	}
	handle := state.Handles[receipt.Token]
	if handle == nil || handle.Lease != receipt.Lease || handle.Instance != receipt.Instance {
		return agent.ErrClaimLost
	}
	return nil
}
