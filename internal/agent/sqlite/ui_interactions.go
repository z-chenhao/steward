package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/tool"
)

// resolveUIComponent is the sole authorization for component state and actions.
// Unpublished artifacts, old executable HTML and other Agent scopes fail closed.
func resolveUIComponent(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ref string) (ui.Component, error) {
	base, index, found := strings.Cut(ref, "#")
	i, err := strconv.Atoi(index)
	if !found || err != nil || i < 0 || strconv.Itoa(i) != index || len(ref) > 256 {
		return ui.Component{}, agent.ErrNotFound
	}
	var raw, receipt string
	var managed bool
	err = q.QueryRowContext(ctx, `SELECT `+uiStoredColumns+` FROM ui_widget w JOIN run r USING(run_id) JOIN conversation_intent c ON c.message_id=w.message_id JOIN tool_invocation t USING(invocation_id) WHERE w.ui_ref=? AND r.kind='main' AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.status='succeeded'`, base).Scan(&raw, &managed, &receipt)
	if err != nil {
		return ui.Component{}, agent.ErrNotFound
	}
	var w ui.Widget
	valid := false
	if managed {
		var decodeErr error
		w, _, decodeErr = storedUI(raw, true, receipt)
		valid = decodeErr == nil
	} else {
		// Legacy executable HTML remains excluded from interactive authority.
		w, valid = ui.ParseWidget(raw)
	}
	if !valid || i >= len(w.Components) {
		return ui.Component{}, agent.ErrNotFound
	}
	return w.Components[i], nil
}
func (s *Store) WebUIState(ctx context.Context, ref string) (ui.ViewState, error) {
	if _, err := resolveUIComponent(ctx, s.db, ref); err != nil {
		return ui.ViewState{}, err
	}
	state := ui.ViewState{State: json.RawMessage(`{}`), ModelContext: json.RawMessage(`{}`)}
	var stateJSON, contextJSON string
	err := s.db.QueryRowContext(ctx, `SELECT version,state_json,model_context_json FROM ui_view_state WHERE component_ref=?`, ref).Scan(&state.Version, &stateJSON, &contextJSON)
	if err == nil {
		state.State = json.RawMessage(stateJSON)
		state.ModelContext = json.RawMessage(contextJSON)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	return state, err
}
func (s *Store) SaveWebUIState(ctx context.Context, ref string, state ui.ViewState) (ui.ViewState, error) {
	if !ui.ValidViewState(state) {
		return state, conversation.ErrInvalid
	}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if _, err := resolveUIComponent(ctx, tx, ref); err != nil {
			return err
		}
		var version int64
		err := tx.QueryRowContext(ctx, `SELECT version FROM ui_view_state WHERE component_ref=?`, ref).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if version != state.Version {
			return conversation.ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ui_view_state(component_ref,state_json,model_context_json,version,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(component_ref) DO UPDATE SET state_json=excluded.state_json,model_context_json=excluded.model_context_json,version=excluded.version,updated_at=excluded.updated_at`, ref, string(state.State), string(state.ModelContext), version+1, time.Now().UnixMicro()); err != nil {
			return err
		}
		state.Version++
		return nil
	})
	return state, err
}

type uiActionInput struct {
	DefinitionHash string
	Ref, Source    string
	Action         ui.ActionProps
}

func (r *Runs) SubmitUIAction(ctx context.Context, ref, source string) (agent.Receipt, error) {
	if len(source) < 16 || len(source) > 100 || strings.ContainsAny(source, "/\\\r\n \x00") {
		return agent.Receipt{}, conversation.ErrInvalid
	}
	c, err := resolveUIComponent(ctx, r.host.db, ref)
	if err != nil || c.Name != "steward.action" {
		return agent.Receipt{}, conversation.ErrInvalid
	}
	var action ui.ActionProps
	if json.Unmarshal(c.Props, &action) != nil {
		return agent.Receipt{}, conversation.ErrInvalid
	}
	allowed := false
	for _, spec := range agent.MainTools(r.registry.Describe()) {
		if spec.Name == action.Tool && !spec.Internal && spec.Permission != "ui_present" {
			allowed = true
			break
		}
	}
	if !allowed {
		return agent.Receipt{}, conversation.ErrInvalid
	}
	e := interaction.InboundEvent{Channel: "web", Subject: "browser", Destination: "local", SourceID: "ui-action:" + source, EventID: "ui-action:" + source, Content: conversation.Text(action.Label)}
	return r.submit(ctx, "", e.Content, &e, "", &uiActionInput{Ref: ref, Source: source, Action: action, DefinitionHash: ui.Hash(c.Props)}, nil)
}

// freezeUIInput takes the snapshot in the ordinary input transaction, once.
// It is user evidence, never a mutable system prompt or an execution bypass.
func (r *Runs) freezeUIInput(ctx context.Context, tx *sql.Tx, messageID string, action *uiActionInput) error {
	type componentContext struct {
		Ref     string          `json:"component_ref"`
		Context json.RawMessage `json:"context"`
	}
	var contextData []componentContext
	rows, err := tx.QueryContext(ctx, `SELECT v.component_ref,v.model_context_json FROM ui_view_state v JOIN ui_widget w ON w.ui_ref=substr(v.component_ref,1,instr(v.component_ref,'#')-1) JOIN run r USING(run_id) WHERE r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND v.model_context_json!='{}' AND v.updated_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)),0) ORDER BY v.updated_at DESC,v.component_ref LIMIT 8`)
	if err != nil {
		return err
	}
	size := 0
	for rows.Next() {
		var item componentContext
		var rawContext string
		if err = rows.Scan(&item.Ref, &rawContext); err != nil {
			rows.Close()
			return err
		}
		item.Context = json.RawMessage(rawContext)
		size += len(item.Context)
		if size <= 16<<10 {
			contextData = append(contextData, item)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	messages := []agent.AgentMessage{}
	if len(contextData) > 0 {
		messages = append(messages, agent.RuntimeContext("ui_state", messageID, false, struct {
			Description string             `json:"description"`
			Components  []componentContext `json:"components"`
		}{"Persisted component state, possibly from older topics; this is not the active page or the subject of the current request. Use it only when relevant to the user's explicit request.", contextData}))
	}
	if action != nil {
		// Revalidate the exact delivered definition inside the acceptance transaction.
		c, e := resolveUIComponent(ctx, tx, action.Ref)
		if e != nil || ui.Hash(c.Props) != action.DefinitionHash {
			return conversation.ErrConflict
		}
		if json.Unmarshal(c.Props, &action.Action) != nil {
			return conversation.ErrInvalid
		}
		if err := bindUIArguments(ctx, tx, action); err != nil {
			return err
		}
		var specsJSON string
		if err := tx.QueryRowContext(ctx, `SELECT c.tools_json FROM main_input i JOIN run r USING(run_id) JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id WHERE i.message_id=?`, messageID).Scan(&specsJSON); err != nil {
			return err
		}
		var specs []tool.Spec
		if json.Unmarshal([]byte(specsJSON), &specs) != nil {
			return agent.ErrStorage
		}
		allowed := false
		for _, spec := range specs {
			if spec.Name == action.Action.Tool && !spec.Internal && spec.Permission != "ui_present" && tool.Validate(spec.InputSchema, string(action.Action.Arguments)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return conversation.ErrInvalid
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ui_action_request(message_id,component_ref,source_id,definition_hash,tool_name,arguments_json) VALUES(?,?,?,?,?,?)`, messageID, action.Ref, action.Source, action.DefinitionHash, action.Action.Tool, string(action.Action.Arguments)); err != nil {
			return err
		}
		messages = append(messages, agent.RuntimeContext("ui_action_request", action.Ref, false, struct {
			RequestID string          `json:"request_id"`
			Tool      string          `json:"tool"`
			Arguments json.RawMessage `json:"arguments"`
		}{action.Source, action.Action.Tool, action.Action.Arguments}))
	}
	if len(messages) == 0 {
		return nil
	}
	raw, _ := json.Marshal(messages)
	_, err = tx.ExecContext(ctx, `INSERT INTO main_ui_context(message_id,context_json) VALUES(?,?)`, messageID, string(raw))
	return err
}

func (s *Store) WebUIActionResult(ctx context.Context, ref, source string) (ui.ActionResult, error) {
	if _, err := resolveUIComponent(ctx, s.db, ref); err != nil {
		return ui.ActionResult{}, err
	}
	var runID, messageID, name, args, runStatus string
	var floor, ceiling sql.NullInt64
	var reply sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT i.run_id,i.message_id,a.tool_name,a.arguments_json,a.step_floor,a.step_ceiling,a.reply_message_id,r.status FROM ui_action_request a JOIN main_input i USING(message_id) JOIN run r USING(run_id) WHERE a.component_ref=? AND a.source_id=? AND r.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)`, ref, source).Scan(&runID, &messageID, &name, &args, &floor, &ceiling, &reply, &runStatus)
	if err != nil {
		return ui.ActionResult{}, agent.ErrNotFound
	}
	out := ui.ActionResult{Status: "pending", MessageID: messageID}
	if floor.Valid {
		rows, e := s.db.QueryContext(ctx, `SELECT t.invocation_id,t.arguments_json,t.status,t.result_json FROM tool_invocation t JOIN model_generation g ON g.generation_id=t.provider_exchange_id AND g.run_id=t.run_id WHERE t.run_id=? AND t.tool_name=? AND g.step_index>=? AND (? IS NULL OR g.step_index<=?) ORDER BY g.step_index,t.created_at,t.delivery_seq`, runID, name, floor.Int64, ceiling, ceiling)
		if e != nil {
			return out, e
		}
		defer rows.Close()
		for rows.Next() {
			var id, actual, status string
			var raw sql.NullString
			if e = rows.Scan(&id, &actual, &status, &raw); e != nil {
				return out, e
			}
			if ui.Hash(json.RawMessage(actual)) != ui.Hash(json.RawMessage(args)) {
				continue
			}
			out.InvocationID = id
			if status == "succeeded" || status == "failed" || status == "unknown" || status == "cancelled" {
				if !raw.Valid {
					result := tool.Failure("RESULT_UNAVAILABLE")
					out.Result = &result
					out.Status = status
					return out, nil
				}
				var result tool.Result
				if json.Unmarshal([]byte(raw.String), &result) != nil {
					return out, agent.ErrStorage
				}
				out.Result = &result
				out.Status = status
				return out, nil
			}
			if status == "waiting" {
				out.Status = "waiting"
			}
			return out, nil
		}
		if e = rows.Err(); e != nil {
			return out, e
		}
	}
	if ceiling.Valid || runStatus == "completed" || runStatus == "failed" || runStatus == "cancelled" {
		result := tool.Failure("ACTION_NOT_EXECUTED")
		out.Status = "failed"
		out.Result = &result
	}
	return out, nil
}

// A binding reads a declared value from another component in the same artifact.
// It cannot select tools, execute expressions, traverse Host objects or mutate
// accepted arguments. The resulting call is frozen with this input transaction.
func bindUIArguments(ctx context.Context, tx *sql.Tx, input *uiActionInput) error {
	if len(input.Action.Bindings) == 0 {
		return nil
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(input.Action.Arguments, &args) != nil {
		return conversation.ErrInvalid
	}
	base, _, _ := strings.Cut(input.Ref, "#")
	for _, binding := range input.Action.Bindings {
		ref := base + "#" + strconv.Itoa(binding.Component)
		if _, err := resolveUIComponent(ctx, tx, ref); err != nil {
			return conversation.ErrInvalid
		}
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT state_json FROM ui_view_state WHERE component_ref=?`, ref).Scan(&raw)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		var value any
		if json.Unmarshal([]byte(raw), &value) != nil {
			return agent.ErrStorage
		}
		found := true
		for _, part := range strings.Split(strings.TrimPrefix(binding.Path, "/"), "/") {
			part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch node := value.(type) {
			case map[string]any:
				value, found = node[part]
			case []any:
				i, e := strconv.Atoi(part)
				if e != nil || i < 0 || i >= len(node) {
					found = false
				} else {
					value = node[i]
				}
			default:
				found = false
			}
			if !found {
				break
			}
		}
		if found {
			args[binding.Argument], _ = json.Marshal(value)
		}
	}
	raw, err := json.Marshal(args)
	if err != nil || len(raw) > 8192 {
		return conversation.ErrInvalid
	}
	input.Action.Arguments = raw
	return nil
}
