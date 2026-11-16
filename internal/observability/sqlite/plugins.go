package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	rsisqlite "github.com/z-chenhao/assistant/internal/rsi/sqlite"
)

// ConfigureTracePlugins installs composition only. Read requests never install,
// activate, update projections or write plugin state through this read-only pool.
func (s *Inspector) ConfigureTracePlugins(c rsi.Catalog) {
	if s.plugins == nil {
		s.plugins = &atomic.Pointer[rsi.Catalog]{}
	}
	s.plugins.Store(&c)
}

func (s Inspector) ProjectedObservations(ctx context.Context, q observability.ObservationQuery) (observability.ObservationTree, *observability.TracePresentation, error) {
	if err := q.Validate(); err != nil {
		return observability.ObservationTree{}, nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return observability.ObservationTree{}, nil, err
	}
	defer tx.Rollback()
	tree, err := s.observations(ctx, tx, q)
	if err != nil {
		return tree, nil, err
	}
	host, revision, managed, err := s.traceReadSelection(ctx, tx, q.ReaderRevision)
	if err != nil || !managed {
		return tree, nil, err
	}
	binding, err := host.ServiceInstance(ctx, revision, contract.Contract)
	if err != nil {
		return tree, nil, err
	}
	pkg, err := host.Catalog.Load(binding.PackageDigest)
	if err != nil {
		return tree, nil, err
	}
	legacy := pkg.Manifest.PluginID == contract.PluginID && pkg.Manifest.Version == "1.0.1"
	facts := contract.Facts{Started: parseTime(tree.Trace.CreatedAt), Now: time.Now().UnixMicro(), SelectedType: tree.Selected.Type, SelectedName: tree.Selected.Name, Items: []contract.Observation{}, Timings: []contract.Timing{}}
	if tree.Trace.EndedAt != "" {
		v := parseTime(tree.Trace.EndedAt)
		facts.Ended = &v
	}
	if !legacy {
		facts.SelectedID = tree.Selected.ID
		facts.SelectedStatus = tree.Selected.Status
		if tree.Generation != nil {
			facts.Usage = &contract.Usage{KnownUsage: tree.Generation.Metrics.KnownUsage, OutputTokens: tree.Generation.Metrics.Output}
		}
	}
	for _, o := range tree.Items {
		item := contract.Observation{ID: o.ID, Parent: o.Parent, Started: o.Started, Ended: o.Ended}
		if !legacy {
			item.Type, item.Name, item.Status = o.Type, o.Name, o.Status
		}
		facts.Items = append(facts.Items, item)
	}
	for _, m := range tree.ProviderTimings {
		facts.Timings = append(facts.Timings, contract.Timing{Attempt: m.Attempt, Phase: m.Phase, At: m.At})
	}
	data, err := json.Marshal(facts)
	if err != nil {
		return tree, nil, err
	}
	raw, err := host.ReadService(ctx, revision, contract.Contract, json.RawMessage(`{"input_id":"observations"}`), []rsi.InputData{{ID: "observations", Data: data}})
	if err != nil {
		return tree, nil, err
	}
	var layout contract.Layout
	if err = json.Unmarshal(raw, &layout); err != nil {
		return tree, nil, err
	}
	// Only the released1.0.1 layout can omit the current display decisions.
	if layout.Timeline == nil && !legacy {
		return tree, nil, rsi.ErrInvalid
	}
	presentation, err := validateLayout(tree, layout)
	if presentation != nil {
		presentation.ReaderRevision = revision
	}
	return tree, presentation, err
}

func parseTime(raw string) int64 { at, _ := time.Parse(time.RFC3339Nano, raw); return at.UnixMicro() }

func validateLayout(tree observability.ObservationTree, layout contract.Layout) (*observability.TracePresentation, error) {
	sources := map[string]observability.Observation{}
	for _, o := range tree.Items {
		if _, ok := sources[o.ID]; !ok {
			sources[o.ID] = o
		}
	}
	if len(layout.Nodes) != len(sources) || len(layout.Tabs) == 0 || len(layout.Tabs) > 4 || layout.Tabs[0] != "overview" || layout.TTFTMS != nil && *layout.TTFTMS < 0 {
		return nil, rsi.ErrInvalid
	}
	out := &observability.TracePresentation{FirstOutputAt: layout.FirstOutputAt, OutputRate: layout.OutputRate, Rows: []observability.TraceRow{}, Tabs: layout.Tabs, TTFTMS: layout.TTFTMS}
	if layout.FirstOutputAt != nil {
		found := false
		for _, mark := range tree.ProviderTimings {
			found = found || mark.At == *layout.FirstOutputAt
		}
		if !found || *layout.FirstOutputAt <= 0 {
			return nil, rsi.ErrInvalid
		}
	}
	if layout.OutputRate != nil && (math.IsNaN(*layout.OutputRate) || math.IsInf(*layout.OutputRate, 0) || *layout.OutputRate < 0 || tree.Generation == nil || tree.Generation.Metrics.KnownUsage == 0) {
		return nil, rsi.ErrInvalid
	}
	if layout.Timeline != nil {
		if layout.Timeline.End <= layout.Timeline.Start || layout.Timeline.End-layout.Timeline.Start <= 0 {
			return nil, rsi.ErrInvalid
		}
		out.Timeline = &observability.TraceTimeline{Start: layout.Timeline.Start, End: layout.Timeline.End}
	}
	seen := map[string]bool{}
	for _, n := range layout.Nodes {
		o, ok := sources[n.ID]
		if !ok || seen[n.ID] || n.Depth < 0 || n.Depth > 16 || len(n.Guides) > 16 || math.IsNaN(n.X) || math.IsNaN(n.Width) || n.X < 0 || n.Width < 0 || !n.Running && n.Width < 4 || n.Running && (o.Ended != nil || tree.Trace.EndedAt != "") || n.X+n.Width > 1000 {
			return nil, rsi.ErrInvalid
		}
		seen[n.ID] = true
		out.Rows = append(out.Rows, observability.TraceRow{Primary: n.Primary, PrimaryHasChildren: n.PrimaryHasChildren, Running: n.Running, Observation: o, Depth: n.Depth, ParentOutside: n.ParentOutside, HasChildren: n.HasChildren, Guides: n.Guides, X: n.X, Width: n.Width})
	}
	seen = map[string]bool{}
	for _, tab := range layout.Tabs {
		if seen[tab] {
			return nil, rsi.ErrInvalid
		}
		seen[tab] = true
		switch tab {
		case "overview", "context", "wire", "diff":
		default:
			return nil, errors.New("invalid trace view tab")
		}
	}
	return out, nil
}

func (s Inspector) traceService(ctx context.Context, tx *sql.Tx, service string, data []byte, requested string) (json.RawMessage, string, bool, error) {
	host, revision, managed, err := s.traceReadSelection(ctx, tx, requested)
	if err != nil || !managed {
		return nil, revision, managed, err
	}
	raw, err := host.ReadService(ctx, revision, service, json.RawMessage(`{"input_id":"observations"}`), []rsi.InputData{{ID: "observations", Data: data}})
	return raw, revision, true, err
}

func (s Inspector) traceReadSelection(ctx context.Context, tx *sql.Tx, requested string) (rsi.Host, string, bool, error) {
	managed, err := s.traceManaged(ctx, tx)
	if err != nil {
		return rsi.Host{}, "", false, err
	}
	if !managed {
		if requested != "" {
			return rsi.Host{}, "", false, rsi.ErrNotFound
		}
		return rsi.Host{}, "", false, nil
	}
	var catalog *rsi.Catalog
	if s.plugins != nil {
		catalog = s.plugins.Load()
	}
	if catalog == nil {
		return rsi.Host{}, "", true, rsi.ErrNotFound
	}
	var revision, owner string
	var epoch int64
	err = tx.QueryRowContext(ctx, `SELECT coalesce(c.active_graph,''),a.agent_id,coalesce(p.epoch,0) FROM rsi_control c CROSS JOIN agent_config a LEFT JOIN memory_privacy p ON p.agent_id=a.agent_id WHERE c.singleton=1 AND a.singleton=1`).Scan(&revision, &owner, &epoch)
	if err != nil {
		return rsi.Host{}, "", true, err
	}
	if revision == "" && requested == "" {
		return rsi.Host{}, "", true, rsi.ErrRevoked
	}
	if requested != "" {
		revision = requested
	}
	store := &rsisqlite.Store{DB: s.guard, Fence: func(ctx context.Context, check *sql.Tx) error {
		var current int64
		var agent string
		if err := check.QueryRowContext(ctx, `SELECT a.agent_id,coalesce(p.epoch,0) FROM agent_config a LEFT JOIN memory_privacy p ON p.agent_id=a.agent_id WHERE a.singleton=1`).Scan(&agent, &current); err != nil {
			return err
		}
		if current != epoch || agent != owner {
			return rsi.ErrRevoked
		}
		return nil
	}}
	host := rsi.Host{Catalog: *catalog, Store: store}
	return host, revision, true, nil
}
