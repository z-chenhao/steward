package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"github.com/z-chenhao/assistant/internal/skill"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const learnCorrection = "/learn-correction "

type correctionProposal struct {
	request  skill.ReviewRequest
	original string
	count    int
	policy   experiencePolicy
}

// A user opts in per marked CLI/Web message. Other channels and normal user
// prose never pass private text to the experience plugin.
func (r *Runs) planCorrection(ctx context.Context, parts []conversation.ContentPart, inbound *interaction.InboundEvent) (*correctionProposal, error) {
	if len(parts) != 1 || parts[0].Type != "text" || !strings.HasPrefix(parts[0].Text, learnCorrection) {
		return nil, nil
	}
	if inbound != nil {
		if inbound.Channel != "web" {
			return nil, nil
		}
		if e := r.host.CheckChannel(ctx, inbound.Channel, inbound.Subject, inbound.Destination); e != nil {
			return nil, e
		}
	}
	raw := strings.TrimPrefix(parts[0].Text, learnCorrection)
	if len(raw) > 8192 || memory.HasSecret(raw) {
		return nil, agent.ErrConflict
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) < 4 || len(lines) > 18 {
		return nil, agent.ErrConflict
	}
	name, description := strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if lines[0] != name {
		return nil, agent.ErrConflict
	}
	steps := lines[2:]
	q := skill.ReviewRequest{Operation: "propose", Name: name, Description: description, Procedure: strings.Join(steps, "\n"), Reason: "correction", SourceRefs: []string{"conversation:authorized"}}
	if !q.Valid() {
		return nil, agent.ErrConflict
	}
	policy, e := r.prepareExperiencePolicy(ctx, "correction")
	if e != nil {
		return nil, e
	}
	if !policy.Managed || r.plugins == nil {
		return nil, errExperienceUnavailable
	}
	input, _ := json.Marshal(contract.CorrectionInput{Steps: steps})
	out, e := r.plugins.ReadService(ctx, policy.Revision, contract.CorrectionRecipe, input, nil)
	if e != nil {
		return nil, e
	}
	var plan contract.CorrectionPlan
	if rsiplugin.DecodeResult(out, &plan) != nil || plan.IndependentRuns < 2 || plan.IndependentRuns > 16 {
		return nil, agent.ErrConflict
	}
	q.Procedure = plan.Procedure
	if policy.Introduction != "" {
		q.Procedure = policy.Introduction + "\n\n" + q.Procedure
	}
	if !q.Valid() {
		return nil, agent.ErrConflict
	}
	q.SourceRefs = nil
	return &correctionProposal{q, plan.Procedure, plan.IndependentRuns, policy}, nil
}

func (r *Runs) materializeCorrection(ctx context.Context, tx *sql.Tx, owner string, p *correctionProposal) error {
	if p == nil {
		return nil
	}
	var current bool
	if e := tx.QueryRowContext(ctx, "SELECT stopped=0 AND active_graph=? FROM rsi_control WHERE singleton=1", p.policy.Revision).Scan(&current); e != nil {
		return e
	}
	if !current {
		return errExperienceUnavailable
	}
	if e := experiencePolicyFence(ctx, tx, p.policy); e != nil {
		return e
	}
	// Keep a rejected method rejected even after another report or plugin
	// upgrade. A distinct owner-selected key starts a different method.
	var existing bool
	if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM skill_candidate WHERE agent_id=? AND name=? AND reason='correction')`, owner, p.request.Name).Scan(&existing); e != nil || existing {
		return e
	}
	prefix := learnCorrection + p.request.Name + "\n"
	rows, e := tx.QueryContext(ctx, `WITH numbered AS (
	SELECT ci.message_id,ci.sequence,row_number() OVER(PARTITION BY i.run_id ORDER BY ci.sequence) AS position
	FROM main_input i JOIN conversation_intent ci USING(message_id)
	WHERE i.agent_id=? AND ci.sequence>coalesce((SELECT history_floor FROM memory_privacy WHERE agent_id=?),0)
	AND json_extract(ci.message_json,'$.role')='user' AND json_extract(ci.message_json,'$.source') IN ('cli','web')
	AND json_array_length(ci.message_json,'$.content')=1 AND substr(json_extract(ci.message_json,'$.content[0].text'),1,length(?))=?
	) SELECT message_id FROM numbered WHERE position=1 ORDER BY sequence LIMIT ?`, owner, owner, prefix, prefix, p.count)
	if e != nil {
		return e
	}
	q := p.request
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		q.SourceRefs = append(q.SourceRefs, "conversation:"+id)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	if e = rows.Close(); e != nil {
		return e
	}
	if len(q.SourceRefs) != p.count {
		return nil
	}
	_, e = r.proposeExperience(ctx, tx, owner, q, p.original, p.policy)
	return e
}

// Forget keeps original Conversation evidence while closing access to learned
// proposals whose explicitly selected source now falls below the privacy floor.
const forgottenCorrection = `EXISTS(SELECT 1 FROM json_each(skill_candidate.source_refs) refs
 JOIN conversation_intent ci ON refs.value='conversation:'||ci.message_id
 WHERE substr(json_extract(ci.message_json,'$.content[0].text'),1,length('/learn-correction '))='/learn-correction '
 AND ci.sequence<=coalesce((SELECT history_floor FROM memory_privacy WHERE agent_id=skill_candidate.agent_id),0))`
