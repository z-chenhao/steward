package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	webextract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (r *Runs) ConfigurePlugins(c rsi.Catalog) {
	r.host.pluginCatalog.Store(&c)
	r.plugins = &rsi.Host{Catalog: c, Store: r.host.Plugins()}
}
func (r *Runs) pluginHost(c agent.RunClaim) rsi.Host {
	h := *r.plugins
	s := r.host.Plugins()
	s.Fence = func(ctx context.Context, tx *sql.Tx) error { return fence(ctx, tx, c) }
	h.Store = s
	return h
}
func (r *Runs) PluginHook(ctx context.Context, c agent.RunClaim, input rsiplugin.HookInput) (rsiplugin.HookOutput, error) {
	if r.plugins == nil {
		return rsiplugin.HookOutput{}, nil
	}
	if input.Execution.RunID != c.RunID() {
		return rsiplugin.HookOutput{}, agent.ErrClaimLost
	}
	return r.pluginHost(c).Hook(ctx, input)
}
func (r *Runs) pluginModel(ctx context.Context, c agent.RunClaim, run agent.Run, request *llm.GenerationRequest) error {
	if r.plugins == nil {
		return nil
	}
	messages := []rsiplugin.Message{}
	for _, m := range request.Messages {
		item := rsiplugin.Message{Kind: m.Role, Text: m.Text}
		if m.Source != nil {
			item.SourceRef = m.Source.Ref
			item.Protected = m.Source.Protected
		}
		messages = append(messages, item)
	}
	for _, stage := range []rsiplugin.Stage{rsiplugin.BuildContext, rsiplugin.Recall, rsiplugin.CompactContext, rsiplugin.SelectCapabilities, rsiplugin.BeforeModelRequest} {
		names := []string{}
		for _, spec := range request.Tools {
			names = append(names, spec.Name)
		}
		out, err := r.PluginHook(ctx, c, rsiplugin.HookInput{Stage: stage, Execution: rsiplugin.Execution{RunID: run.ID, Role: string(run.Kind), Step: run.Checkpoint.Step}, Messages: messages, ToolNames: names})
		if err != nil {
			return err
		}
		if out.Reject {
			return tool.ErrApprovalDenied
		}
		if out.ToolNames != nil {
			selected := map[string]bool{}
			for _, name := range *out.ToolNames {
				if selected[name] {
					return rsi.ErrInvalid
				}
				found := false
				for _, spec := range request.Tools {
					found = found || spec.Name == name
				}
				if !found {
					return tool.ErrApprovalDenied
				}
				selected[name] = true
			}
			kept := request.Tools[:0]
			for _, spec := range request.Tools {
				if selected[spec.Name] {
					kept = append(kept, spec)
				}
			}
			request.Tools = kept
		}
		for _, m := range out.AdditionalContext {
			request.Messages = append(request.Messages, llm.Message{Role: "user", Text: "<untrusted_plugin_context>\n" + m.Text + "\n</untrusted_plugin_context>"})
		}
	}
	return nil
}

func (r *Runs) pluginSpecs(ctx context.Context, tx *sql.Tx, base []tool.Spec) ([]tool.Spec, error) {
	base, err := r.mcpNamespaceSpecs(ctx, tx, base)
	var namespace *mcpNamespaceNeed
	if err != nil && !errors.As(err, &namespace) {
		return nil, err
	}
	preparations := []error{}
	if namespace != nil {
		preparations = append(preparations, namespace)
	}
	base, err = r.appsGuidanceSpecs(ctx, tx, base)
	var apps *appsGuidanceNeed
	if err != nil && !errors.As(err, &apps) {
		return nil, err
	}
	if apps != nil {
		preparations = append(preparations, apps)
	}
	base, err = r.uiDefinitionSpecs(ctx, tx, base)
	var uiDefinition *uiDefinitionNeed
	if err != nil && !errors.As(err, &uiDefinition) {
		return nil, err
	}
	if uiDefinition != nil {
		preparations = append(preparations, uiDefinition)
	}
	base, err = r.scheduleGuidanceSpecs(ctx, tx, base)
	var schedule *scheduleGuidanceNeed
	if err != nil && !errors.As(err, &schedule) {
		return nil, err
	}
	if schedule != nil {
		preparations = append(preparations, schedule)
	}
	// Real projection consumers prepare after one rollback. Preserve the
	// original three transaction attempts rather than spend one per feature.
	if len(preparations) != 0 {
		return nil, errors.Join(preparations...)
	}
	hasWeb := false
	for _, spec := range base {
		if webToolName(spec.Name) {
			hasWeb = true
			break
		}
	}
	if hasWeb {
		extract, e := managedService(ctx, tx, webextract.Contract)
		if e != nil {
			return nil, e
		}
		if extract {
			for i := range base {
				if webToolName(base[i].Name) {
					base[i].ResourceHash = r.webAccess.ScopeIdentity()
				}
			}
		}
	}
	if r.plugins == nil {
		return base, nil
	}
	var raw, revision string
	err = tx.QueryRowContext(ctx, `SELECT g.graph_json,g.revision FROM rsi_control c JOIN rsi_graph g ON g.revision=c.active_graph WHERE c.singleton=1`).Scan(&raw, &revision)
	if err == sql.ErrNoRows {
		return base, nil
	}
	if err != nil {
		return nil, err
	}
	var g rsiplugin.Graph
	if json.Unmarshal([]byte(raw), &g) != nil {
		return nil, agent.ErrStorage
	}
	for _, b := range g.Bindings {
		resourceHash, err := pluginToolResource(revision, b)
		if err != nil {
			return nil, err
		}
		p, e := r.plugins.Catalog.Load(b.PackageDigest)
		if e != nil {
			return nil, e
		}
		for _, t := range p.Manifest.Tools {
			for _, s := range p.Manifest.Provides {
				if s.Contract == t.Contract {
					base = append(base, tool.Spec{Name: t.Name, Description: t.Description, InputSchema: string(s.InputSchema), ResourceHash: resourceHash, Version: 1, Permission: "read_only", ReplayPolicy: "safe", TimeoutMS: p.Manifest.Runtime.TimeoutMS, OutputBytes: 65536})
				}
			}
		}
	}
	return base, nil
}
func (r *Runs) RegistryForRun(ctx context.Context, c agent.RunClaim, base *tool.Registry) (*tool.Registry, error) {
	var err error
	base, err = r.webRegistryForRun(ctx, c, base)
	if err != nil {
		return nil, err
	}
	base, err = r.uiRegistryForRun(ctx, c, base)
	if err != nil {
		return nil, err
	}
	base, err = r.appsRegistryForRun(ctx, c, base)
	if err != nil {
		return nil, err
	}
	base, err = r.scheduleRegistryForRun(ctx, c, base)
	if err != nil {
		return nil, err
	}
	if r.plugins == nil {
		return base, nil
	}
	h := r.pluginHost(c)
	revision, err := h.Store.Binding(ctx, c.RunID())
	if err != nil {
		return nil, err
	}
	if revision == "" {
		return base, nil
	}
	g, err := h.Store.Graph(ctx, revision)
	if err != nil {
		return nil, err
	}
	defs := []tool.Definition{}
	for _, s := range base.Describe() {
		if d, ok := base.Resolve(s.Name); ok {
			defs = append(defs, d)
		}
	}
	for _, b := range g.Graph.Bindings {
		resourceHash, err := pluginToolResource(revision, b)
		if err != nil {
			return nil, err
		}
		p, e := h.Catalog.Load(b.PackageDigest)
		if e != nil {
			return nil, e
		}
		for _, contribution := range p.Manifest.Tools {
			for _, s := range p.Manifest.Provides {
				if s.Contract != contribution.Contract {
					continue
				}
				binding, t, service := b, contribution, s
				defs = append(defs, tool.Definition{Spec: tool.Spec{Name: t.Name, Description: t.Description, InputSchema: string(service.InputSchema), ResourceHash: resourceHash, Version: 1, Permission: "read_only", ReplayPolicy: "safe", TimeoutMS: p.Manifest.Runtime.TimeoutMS, OutputBytes: 65536}, Execute: func(ctx context.Context, args string) tool.Result {
					key := tool.AcceptedInvocationID(ctx)
					if key == "" {
						return tool.Failure("PERMISSION_DENIED")
					}
					value, err := h.Call(ctx, rsiplugin.Execution{RunID: c.RunID()}, binding.InstanceID, t.Contract, "tool:"+key, json.RawMessage(args))
					if err != nil {
						if errors.Is(err, rsi.ErrUnknown) {
							return tool.Failure("UNKNOWN_SIDE_EFFECT")
						}
						return tool.Failure("PROVIDER_UNAVAILABLE")
					}
					return tool.Result{Text: string(value)}
				}})
			}
		}
	}
	return tool.NewRegistry(defs)
}

func pluginToolResource(revision string, b rsiplugin.Binding) (string, error) {
	return rsiplugin.Digest("steward.rsi.tool/1", struct {
		Revision string
		Binding  rsiplugin.Binding
	}{revision, b})
}

func (r *Runs) pluginDispatchFence(ctx context.Context, tx *sql.Tx, run string) error {
	// Durable Host authority survives an absent executable catalog. Native
	// executions have no managed graph and remain unaffected by this check.
	var blocked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run b JOIN rsi_control c ON c.singleton=1 WHERE b.run_id=? AND b.graph_revision IS NOT NULL AND (c.stopped=1 OR EXISTS(SELECT 1 FROM rsi_quarantine q WHERE q.revision=b.graph_revision)))`, run).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return rsi.ErrRevoked
	}
	return nil
}
func (r *Runs) pluginBeforeTools(ctx context.Context, c agent.RunClaim, exchange string, calls []llm.ToolCall, guard func(context.Context, *sql.Tx) error) ([]llm.ToolCall, []bool, error) {
	out := append([]llm.ToolCall(nil), calls...)
	reject := make([]bool, len(calls))
	if r.plugins == nil {
		return out, reject, nil
	}
	host := *r.plugins
	store := r.host.Plugins()
	store.Fence = guard
	host.Store = store
	for i, call := range calls {
		stages := []rsiplugin.Stage{rsiplugin.BeforeToolCall}
		if call.Name == agent.RunToolName {
			stages = append([]rsiplugin.Stage{rsiplugin.PrepareDelegation}, stages...)
		}
		for _, stage := range stages {
			p, err := host.Hook(ctx, rsiplugin.HookInput{Stage: stage, Execution: rsiplugin.Execution{RunID: c.RunID()}, ToolCall: &rsiplugin.ToolCall{ID: call.ID, Name: call.Name, Arguments: json.RawMessage(out[i].Arguments)}, Reference: exchange})
			if err != nil {
				return nil, nil, err
			}
			reject[i] = reject[i] || p.Reject
			if len(p.Arguments) > 0 {
				out[i].Arguments = string(p.Arguments)
			}
		}
	}
	return out, reject, nil
}

func (r *Runs) enqueuePluginEvent(ctx context.Context, tx *sql.Tx, run string, stage rsiplugin.Stage, version int64, refs ...string) error {
	if r.plugins == nil {
		return nil
	}
	var subscribed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run b JOIN rsi_graph g ON g.revision=b.graph_revision,json_each(g.graph_json,'$.bindings') i JOIN rsi_package p ON p.digest=json_extract(i.value,'$.package_digest'),json_each(p.manifest_json,'$.hooks') h WHERE b.run_id=? AND json_extract(h.value,'$.stage')=?)`, run, string(stage)).Scan(&subscribed); err != nil {
		return err
	}
	if !subscribed {
		return nil
	}
	reference := ""
	if len(refs) > 0 {
		reference = refs[0]
	}
	var status, reason, role string
	var step int64
	if err := tx.QueryRowContext(ctx, `SELECT status,coalesce(wait_reason,error_code,''),kind,json_extract(checkpoint_json,'$.step') FROM run WHERE run_id=?`, run).Scan(&status, &reason, &role, &step); err != nil {
		return err
	}
	input := rsiplugin.HookInput{Stage: stage, Execution: rsiplugin.Execution{RunID: run, Role: role, Step: step}}
	if stage == rsiplugin.ToolResult {
		var call rsiplugin.ToolCall
		var args string
		if err := tx.QueryRowContext(ctx, `SELECT status,coalesce(json_extract(result_json,'$.error.code'),''),version,provider_call_id,tool_name,arguments_json,coalesce(json_extract(result_json,'$.text'),'') FROM tool_invocation WHERE invocation_id=? AND run_id=?`, reference, run).Scan(&status, &reason, &version, &call.ID, &call.Name, &args, &input.Text); err != nil {
			return err
		}
		call.Arguments = json.RawMessage(args)
		input.ToolCall = &call
	}
	key, err := rsiplugin.Digest("steward.rsi.event/1", struct {
		Run       string
		Stage     rsiplugin.Stage
		Version   int64
		Reference string
	}{run, stage, version, reference})
	if err != nil {
		return err
	}
	if reference == "" {
		reference = key
	}
	input.Reference = reference
	input.Status = status
	input.Reason = reason
	_, err = tx.ExecContext(ctx, `INSERT INTO rsi_event(event_id,run_id,stage,step,reference,source_status,reason,input_json,status) VALUES(?,?,?,?,?,?,?,?,'pending') ON CONFLICT DO NOTHING`, key, run, string(stage), step, reference, status, reason, encodePluginInput(input))
	return err
}

func encodePluginInput(input rsiplugin.HookInput) string {
	raw, _ := json.Marshal(input)
	return string(raw)
}
