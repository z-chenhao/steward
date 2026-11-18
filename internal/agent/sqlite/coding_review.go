package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/coding"
)

func (s *Store) readCodeReviewFacts(ctx context.Context, method codeReportMethod) (*coding.ReviewFacts, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, agent.ErrStorage
	}
	defer tx.Rollback()
	if err := codeReportMethodFence(ctx, tx, method); err != nil {
		return nil, err
	}
	facts := &coding.ReviewFacts{TaskEpoch: method.TaskEpoch, InstructionSequence: method.InstructionSequence, Instructions: []coding.Instruction{}}
	var originalArtifacts string
	var created, end int64
	// Use the durable activity watermark, not the reader's clock. Identical
	// accepted facts retain identical report bytes across Artifact upgrades.
	if err := tx.QueryRowContext(ctx, `SELECT t.anchor_task,t.anchor_artifact_refs,t.status,t.created_at,
 max(t.updated_at,
  coalesce((SELECT max(updated_at) FROM run WHERE task_id=t.task_id),t.updated_at),
  coalesce((SELECT max(created_at) FROM task_instruction WHERE task_id=t.task_id),t.updated_at),
  coalesce((SELECT max(u.recorded_at) FROM provider_usage u JOIN run r USING(run_id) WHERE r.task_id=t.task_id),t.updated_at),
  coalesce((SELECT max(i.updated_at) FROM tool_invocation i JOIN run r USING(run_id) WHERE r.task_id=t.task_id),t.updated_at)),
 coalesce(t.completed_at,t.cancelled_at,0) FROM task t WHERE t.task_id=?`, method.TaskID).Scan(&facts.OriginalTask, &originalArtifacts, &facts.TaskStatus, &created, &facts.ObservedAt, &end); err != nil {
		return nil, agent.ErrStorage
	}
	if json.Unmarshal([]byte(originalArtifacts), &facts.OriginalArtifacts) != nil {
		return nil, agent.ErrStorage
	}
	if end == 0 {
		end = facts.ObservedAt
	}
	facts.Metrics.TaskWallMS = max(int64(0), (end-created)/1000)
	rows, err := tx.QueryContext(ctx, `SELECT sequence,task_epoch,instruction,artifact_refs FROM task_instruction WHERE task_id=? ORDER BY sequence DESC LIMIT 33`, method.TaskID)
	if err != nil {
		return nil, agent.ErrStorage
	}
	total := 0
	for rows.Next() {
		var instruction coding.Instruction
		var refs string
		if err := rows.Scan(&instruction.Sequence, &instruction.Epoch, &instruction.Text, &refs); err != nil {
			rows.Close()
			return nil, agent.ErrStorage
		}
		if json.Unmarshal([]byte(refs), &instruction.ArtifactRefs) != nil {
			rows.Close()
			return nil, agent.ErrStorage
		}
		total += len(instruction.Text) + len(refs)
		if len(facts.Instructions) == 32 || total > 64<<10 {
			facts.InstructionsTruncated = true
			break
		}
		facts.Instructions = append(facts.Instructions, instruction)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, agent.ErrStorage
	}
	for i, j := 0, len(facts.Instructions)-1; i < j; i, j = i+1, j-1 {
		facts.Instructions[i], facts.Instructions[j] = facts.Instructions[j], facts.Instructions[i]
	}
	m := &facts.Metrics
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM run WHERE task_id=?`, method.TaskID).Scan(&m.Runs); err != nil {
		return nil, agent.ErrStorage
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM model_generation g JOIN run r USING(run_id) WHERE r.task_id=?`, method.TaskID).Scan(&m.ModelRequests); err != nil {
		return nil, agent.ErrStorage
	}
	var input, output sql.NullInt64
	var inputKnown, outputKnown int
	if err := tx.QueryRowContext(ctx, `SELECT count(*),count(input_tokens),count(output_tokens),sum(input_tokens),sum(output_tokens) FROM provider_usage u JOIN run r USING(run_id) WHERE r.task_id=?`, method.TaskID).Scan(&m.UsageRecords, &inputKnown, &outputKnown, &input, &output); err != nil {
		return nil, agent.ErrStorage
	}
	if input.Valid {
		m.InputTokensReported = &input.Int64
	}
	if output.Valid {
		m.OutputTokensReported = &output.Int64
	}
	m.UsageUnavailable = max(0, m.ModelRequests-m.UsageRecords)
	m.InputTokensMissing = m.UsageRecords - inputKnown + m.UsageUnavailable
	m.OutputTokensMissing = m.UsageRecords - outputKnown + m.UsageUnavailable
	m.Priced = []coding.ReviewCost{}
	rows, err = tx.QueryContext(ctx, `SELECT s.currency,sum(s.actual_micros),count(*) FROM spend_reservation s JOIN model_generation g USING(generation_id) JOIN run r USING(run_id) WHERE r.task_id=? AND s.status='priced' GROUP BY s.currency ORDER BY s.currency`, method.TaskID)
	if err != nil {
		return nil, agent.ErrStorage
	}
	for rows.Next() {
		var cost coding.ReviewCost
		if err := rows.Scan(&cost.Currency, &cost.ActualMicros, &cost.Generations); err != nil {
			rows.Close()
			return nil, agent.ErrStorage
		}
		m.Priced = append(m.Priced, cost)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, agent.ErrStorage
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM provider_usage u JOIN run r USING(run_id) WHERE r.task_id=? AND NOT EXISTS(SELECT 1 FROM spend_reservation s WHERE s.generation_id=u.generation_id AND s.status='priced')`, method.TaskID).Scan(&m.UnpricedUsage); err != nil {
		return nil, agent.ErrStorage
	}
	return facts, nil
}
