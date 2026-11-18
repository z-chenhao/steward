package sqlite

import (
	"context"
	"database/sql"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/tool"
)

// ExecuteBuiltin routes the accepted, persisted Tool name through the shared
// Host execution pipeline. Each capability retains its own transaction/fencing.
func (t *tools) ExecuteBuiltin(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	var name string
	if err := t.runs.host.db.QueryRowContext(ctx, `SELECT tool_name FROM tool_invocation WHERE invocation_id=? AND run_id=?`, inv.ID, t.claim.RunID()).Scan(&name); err != nil {
		return tool.Result{}, agent.ErrStorage
	}
	family, ok := tool.HostFamily(name)
	if !ok {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	switch family {
	case tool.SkillBuiltin:
		return t.executeSkill(ctx, inv)
	case tool.WebAccessBuiltin:
		return t.executeWebAccess(ctx, inv)
	case tool.UIBuiltin:
		return t.showUI(ctx, inv)
	case tool.SearchBuiltin:
		return t.searchTools(ctx, inv)
	case tool.AutomationBuiltin:
		return t.executeAutomation(ctx, inv)
	case tool.CodeBuiltin:
		return t.executeCode(ctx, inv)
	case tool.EnvironmentBuiltin:
		if coding.IsCheckTool(name) {
			return t.executeCodingCheck(ctx, inv)
		}
		return t.executeEnvironment(ctx, inv)
	case tool.ComputerBuiltin:
		return t.executeComputer(ctx, inv)
	case tool.AgentBuiltin:
		return t.AgentToolExecutor(ctx, inv.ID)
	case tool.MemoryWriteBuiltin:
		return t.manageMemory(ctx, inv)
	case tool.MemorySearchBuiltin:
		return t.searchMemory(ctx, inv)
	case tool.EvidenceBuiltin:
		return t.readLinkedEvidence(ctx, inv)
	case tool.TaskBuiltin:
		return t.executeTaskAdmission(ctx, inv)
	case tool.ArtifactBuiltin:
		return t.executeArtifactCall(ctx, inv)
	case tool.ImageBuiltin:
		return t.executeImageOCR(ctx, inv)
	}
	return tool.Failure("PERMISSION_DENIED"), nil
}

// acceptedBuiltin resolves only a running, fenced invocation whose capability
// was frozen for this Run. Shared admission never dispatches an unknown name.
func (t *tools) acceptedBuiltin(ctx context.Context, tx *sql.Tx, inv tool.Invocation) (kind, name, args string, err error) {
	if err = t.fence(ctx, tx); err != nil {
		return
	}
	err = tx.QueryRowContext(ctx, `SELECT r.kind,i.tool_name,i.arguments_json FROM tool_invocation i JOIN run r USING(run_id) WHERE i.invocation_id=? AND i.run_id=? AND i.status='running'`, inv.ID, t.claim.RunID()).Scan(&kind, &name, &args)
	if err != nil {
		return
	}
	var visible int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM run r JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id, json_each(c.tools_json) j WHERE r.run_id=? AND json_extract(j.value,'$.name')=?`, t.claim.RunID(), name).Scan(&visible)
	if err == nil && visible != 1 {
		err = tool.ErrApprovalDenied
	}
	return
}
