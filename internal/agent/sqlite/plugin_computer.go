package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Select the original Run's pure method before acquiring the Computer. There
// is no native projection or mutation dispatch when a managed package is absent.
func (t *tools) computerMethodHost(ctx context.Context, inv tool.Invocation, service string) (*rsi.Host, string, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), service)
	if err != nil || !managed {
		return nil, "", err
	}
	if t.runs.plugins == nil {
		return nil, "", rsi.ErrNotFound
	}
	err = t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		_, name, raw, err := t.acceptedBuiltin(ctx, tx, inv)
		if err != nil {
			return err
		}
		if name != inv.Call.Name || raw != inv.Call.Arguments {
			return tool.ErrReplay
		}
		return t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID())
	})
	if err != nil {
		return nil, "", err
	}
	host := *t.runs.plugins
	store := t.runs.host.Plugins()
	store.Fence = t.computerMethodFence
	host.Store = store
	revision, err := store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return nil, "", err
	}
	if revision == "" {
		return nil, "", rsi.ErrNotFound
	}
	if err = store.CheckGraph(ctx, revision); err != nil {
		return nil, "", err
	}
	if _, err = host.ServiceInstance(ctx, revision, service); err != nil {
		return nil, "", err
	}

	return &host, revision, nil
}

func (t *tools) computerContentHost(ctx context.Context, inv tool.Invocation, service string) (*rsi.Host, string, error) {
	host, revision, err := t.computerMethodHost(ctx, inv, service)
	if err != nil || host == nil {
		return nil, "", err
	}
	// The preflight precedes Ensure; actual text reads require the acquired
	// Computer lease as well as the original Run/Code/Guardian authority.
	store := t.runs.host.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error {
		if err := t.computerMethodFence(ctx, tx); err != nil {
			return err
		}
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computer WHERE execution_id=? AND execution_epoch=? AND phase='ready')`, t.codeOwner.req.ExecutionID, t.codeOwner.req.ExecutionEpoch).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return computer.ErrStale
		}
		return nil
	}
	host.Store = store
	return host, revision, nil
}

func (t *tools) computerObservationProjector(ctx context.Context, inv tool.Invocation) (computer.ObservationProjector, error) {
	host, revision, err := t.computerContentHost(ctx, inv, contract.ObservationContract)
	if err != nil || host == nil {
		return nil, err
	}
	return func(ctx context.Context, input computer.ObservationInput) (computer.ObservationView, error) {
		if len(input.Before) > 128<<10 || len(input.After) > 128<<10 || !utf8.ValidString(input.Before) || !utf8.ValidString(input.After) {
			return computer.ObservationView{}, rsi.ErrInvalid
		}
		facts, _ := json.Marshal(contract.ObservationFacts{DiffAllowed: input.DiffAllowed, Full: input.Full})
		snapshots, _ := json.Marshal(contract.ObservationSnapshots{Before: input.Before, After: input.After})
		raw, err := host.ReadService(ctx, revision, contract.ObservationContract, facts, []rsi.InputData{{ID: "observations", Data: snapshots}})
		if err != nil {
			return computer.ObservationView{}, err
		}
		var plan contract.ObservationPlan
		if rsiplugin.DecodeResult(raw, &plan) != nil {
			return computer.ObservationView{}, rsi.ErrInvalid
		}
		return renderComputerObservation(input, plan)
	}, nil
}

func (t *tools) computerDocumentationReader(ctx context.Context, inv tool.Invocation) (computer.DocumentationReader, error) {
	host, revision, err := t.computerContentHost(ctx, inv, contract.DocumentationContract)
	if err != nil || host == nil {
		return nil, err
	}
	return func(ctx context.Context, kind string) (string, error) {
		if kind != "core" && kind != "browser" {
			return "", rsi.ErrInvalid
		}
		input, _ := json.Marshal(contract.DocumentationFacts{Kind: kind})
		raw, err := host.ReadService(ctx, revision, contract.DocumentationContract, input, nil)
		if err != nil {
			return "", err
		}
		var document contract.Documentation
		if rsiplugin.DecodeResult(raw, &document) != nil || len(document.Text) == 0 || len(document.Text) > 16<<10 || !utf8.ValidString(document.Text) {
			return "", rsi.ErrInvalid
		}
		return document.Text, nil
	}, nil
}

func (t *tools) computerSelectionPlanner(ctx context.Context, inv tool.Invocation) (computer.SelectionPlanner, error) {
	host, revision, err := t.computerContentHost(ctx, inv, contract.SelectionContract)
	if err != nil || host == nil {
		return nil, err
	}
	return func(ctx context.Context, in computer.SelectionInput) (computer.SelectionRecipe, error) {
		// Eligibility and exact selected bytes are independently checked by the
		// Computer owner. The method sees only the existing selectable prefix and
		// total length, never tokens, Native IDs or the rest of the document.
		input, _ := json.Marshal(contract.SelectionFacts{Prefix: in.Value[:min(len(in.Value), 256)], Length: len(in.Value), Wanted: in.Wanted})
		raw, err := host.ReadService(ctx, revision, contract.SelectionContract, input, nil)
		if err != nil {
			return computer.SelectionRecipe{}, err
		}
		var plan computer.SelectionRecipe
		if rsiplugin.DecodeResult(raw, &plan) != nil || !computer.ValidSelection(in, plan) {
			return computer.SelectionRecipe{}, rsi.ErrInvalid
		}
		t.runs.host.boundary("before_computer_selection_dispatch")
		return plan, nil
	}, nil
}

func (t *tools) commitComputerDocumentation(ctx context.Context, tx *sql.Tx, receipt computer.DocumentationReceipt) error {
	if t.codeOwner == nil || receipt.Lease != t.codeOwner.req.ExecutionID {
		return agent.ErrClaimLost
	}
	if err := t.computerMethodFence(ctx, tx); err != nil {
		return err
	}
	var owned bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computer WHERE execution_id=? AND execution_epoch=? AND computer_id=? AND instance_epoch=? AND phase='ready')`, receipt.Lease, t.codeOwner.req.ExecutionEpoch, receipt.Instance.ComputerID, receipt.Instance.Epoch).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return agent.ErrClaimLost
	}
	return nil
}

func (t *tools) computerMethodFence(ctx context.Context, tx *sql.Tx) error {
	if err := t.fence(ctx, tx); err != nil {
		return err
	}
	return t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID())
}

func (t *tools) computerWaitPolicy(ctx context.Context, inv tool.Invocation) (computer.WaitPolicy, error) {
	host, revision, err := t.computerMethodHost(ctx, inv, contract.WaitContract)
	if err != nil || host == nil {
		return nil, err
	}
	var call computer.BridgeCall
	if json.Unmarshal([]byte(inv.Call.Arguments), &call) != nil {
		return nil, rsi.ErrInvalid
	}
	if call.Method != "waitForChange" {
		return func(context.Context, computer.WaitInput) (computer.WaitRecipe, error) {
			return computer.WaitRecipe{}, rsi.ErrInvalid
		}, nil
	}
	// Numeric policy is selected before Ensure/dispatch. No plugin process runs
	// inside the actual wait deadline; explicit user duration remains authoritative.
	facts, _ := json.Marshal(contract.WaitFacts{Ceilings: contract.WaitCeilings{TimeoutMS: 10000, MinPollMS: 100, MaxPollMS: 1000}})
	raw, err := host.ReadService(ctx, revision, contract.WaitContract, facts, nil)
	if err != nil {
		return nil, err
	}
	var plan contract.WaitRecipe
	if rsiplugin.DecodeResult(raw, &plan) != nil || plan.TimeoutMS < 1 || plan.TimeoutMS > 10000 || plan.PollMS < 100 || plan.PollMS > 1000 {
		return nil, rsi.ErrInvalid
	}
	return func(ctx context.Context, input computer.WaitInput) (computer.WaitRecipe, error) {
		if err := ctx.Err(); err != nil {
			return computer.WaitRecipe{}, err
		}
		timeout := plan.TimeoutMS
		if input.RequestedTimeoutMS != nil {
			if *input.RequestedTimeoutMS < 1 || *input.RequestedTimeoutMS > 10000 {
				return computer.WaitRecipe{}, rsi.ErrInvalid
			}
			timeout = *input.RequestedTimeoutMS
		}
		return computer.WaitRecipe{TimeoutMS: timeout, PollMS: plan.PollMS}, nil
	}, nil
}

// Check complete reconstruction, not the candidate's preferred diff algorithm.
// Wider contextual replacements and full snapshots are independently iterable;
// omitted, invented or changed source lines cannot become an observation.
func renderComputerObservation(input computer.ObservationInput, plan contract.ObservationPlan) (computer.ObservationView, error) {
	if plan.Mode == "full" && plan.Start == 0 && plan.Removed == 0 && plan.Added == 0 {
		return computer.ObservationView{Kind: "full", State: input.After}, nil
	}
	if plan.Mode != "diff" || !input.DiffAllowed || input.Full || plan.Start < 0 || plan.Removed < 0 || plan.Added < 0 {
		return computer.ObservationView{}, rsi.ErrInvalid
	}
	if input.Before == input.After {
		if plan.Start == 0 && plan.Removed == 0 && plan.Added == 0 {
			return computer.ObservationView{Kind: "diff"}, nil
		}
		return computer.ObservationView{}, rsi.ErrInvalid
	}
	a, b := strings.Split(input.Before, "\n"), strings.Split(input.After, "\n")
	if plan.Start > len(a) || plan.Start > len(b) || plan.Removed > len(a)-plan.Start || plan.Added > len(b)-plan.Start || !slices.Equal(a[:plan.Start], b[:plan.Start]) || !slices.Equal(a[plan.Start+plan.Removed:], b[plan.Start+plan.Added:]) {
		return computer.ObservationView{}, rsi.ErrInvalid
	}
	lines := []string{fmt.Sprintf("@@ %d,%d,%d @@", plan.Start, plan.Removed, plan.Added)}
	for _, line := range a[plan.Start : plan.Start+plan.Removed] {
		lines = append(lines, "- "+line)
	}
	for _, line := range b[plan.Start : plan.Start+plan.Added] {
		lines = append(lines, "+ "+line)
	}
	return computer.ObservationView{Kind: "diff", State: strings.Join(lines, "\n")}, nil
}
