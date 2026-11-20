package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/codingdraft"
	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func draftDigest(p codingdraft.Plan) (string, []byte) {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), raw
}
func draftChanges(base, after []coding.DraftEntry) []codingdraft.Change {
	before := map[string]coding.DraftEntry{}
	for _, v := range base {
		before[v.Path] = v
	}
	changes := []codingdraft.Change{}
	for _, v := range after {
		old := before[v.Path]
		delete(before, v.Path)
		if old.SHA != v.SHA || old.Mode != v.Mode {
			changes = append(changes, codingdraft.Change{Path: v.Path, Before: old.SHA, After: v.SHA, Mode: v.Mode})
		}
	}
	for _, v := range before {
		changes = append(changes, codingdraft.Change{Path: v.Path, Before: v.SHA})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes
}
func (s *Store) draftPlan(ctx context.Context, remote codingdraft.Remote, ref string, t codingdraft.Target) (codingdraft.Plan, coding.DraftTree, error) {
	var plan codingdraft.Plan
	var tree coding.DraftTree
	if !t.Valid() {
		return plan, tree, codingdraft.ErrInvalid
	}
	report, method, e := s.codeReport(ctx, ref, true)
	if e != nil {
		return plan, tree, e
	}
	if !method.Managed || report.ChecksTruncated || len(report.Checks) == 0 {
		return plan, tree, codingdraft.ErrConflict
	}
	store := s.Plugins()
	store.Fence = func(ctx context.Context, tx *sql.Tx) error { return codeReportMethodFence(ctx, tx, method) }
	catalog := rsi.NewCatalog(s.DataRoot())
	if c := s.pluginCatalog.Load(); c != nil {
		catalog = *c
	}
	facts := contract.DraftFacts{Verified: make([]bool, len(report.Checks))}
	for i, proof := range report.Checks {
		facts.Verified[i] = codingdraft.Proof(proof)
	}
	raw, _ := json.Marshal(facts)
	reply, e := (rsi.Host{Store: store, Catalog: catalog}).ReadService(ctx, method.Revision, contract.DraftContract, raw, nil)
	if e != nil {
		return plan, tree, e
	}
	var recipe contract.DraftRecipe
	if rsiplugin.DecodeResult(reply, &recipe) != nil || recipe.Proof < 0 || recipe.Proof >= len(facts.Verified) || !facts.Verified[recipe.Proof] || recipe.Body == "" {
		return plan, tree, codingdraft.ErrConflict
	}
	proof := report.Checks[recipe.Proof]
	snapshot, e := coding.CaptureCandidate(ctx, report.Workspace)
	if e != nil {
		return plan, tree, e
	}
	defer snapshot.Close()
	if snapshot.SHA256 != report.CandidateSHA256 {
		return plan, tree, codingdraft.ErrConflict
	}
	tree, e = coding.DraftObjects(ctx, snapshot)
	if e != nil {
		return plan, tree, e
	}
	observed, e := remote.Inspect(ctx, t)
	if e != nil {
		return plan, tree, e
	}
	if observed.Branch != "" || observed.Base == "" || observed.RepositoryID < 1 {
		return plan, tree, codingdraft.ErrConflict
	}
	changes := draftChanges(observed.Entries, tree.Entries)
	if len(changes) == 0 {
		return plan, tree, codingdraft.ErrConflict
	}
	plan = codingdraft.Plan{Task: ref, Epoch: method.TaskEpoch, Privacy: method.Epoch, Sequence: method.InstructionSequence, Graph: method.Revision, Target: t, RepositoryID: observed.RepositoryID, BaseSHA: observed.Base, Candidate: snapshot.SHA256, Tree: tree.SHA, Proof: proof.InvocationID, CheckCommand: proof.Command, CheckDriver: proof.DriverHash, CheckMethod: proof.MethodRevision, CheckScope: proof.EnvironmentScope, VerifiedAt: proof.AcceptedAt, Changes: changes, Message: t.Title, Body: recipe.Body + "\n\nCandidate SHA-256: " + snapshot.SHA256}
	return plan, tree, nil
}
func (s *Store) codingDraftFence(ctx context.Context, tx *sql.Tx, p codingdraft.Plan) error {
	var id string
	var privacy, epoch, seq int64
	if e := tx.QueryRowContext(ctx, codeReportScopeSQL, p.Task).Scan(&id, &privacy, &epoch, &seq); e != nil {
		return e
	}
	if privacy != p.Privacy || epoch != p.Epoch || seq != p.Sequence {
		return codingdraft.ErrConflict
	}
	var allowed bool
	e := tx.QueryRowContext(ctx, `SELECT (SELECT status IN ('active','completed') FROM task WHERE task_id=?) AND
	(SELECT stopped=0 AND active_graph=? FROM rsi_control WHERE singleton=1) AND NOT EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?) AND
	EXISTS(SELECT 1 FROM tool_invocation i JOIN run r USING(run_id) WHERE i.invocation_id=? AND i.status='succeeded' AND r.task_id=?)`, id, p.Graph, p.Graph, p.Proof, id).Scan(&allowed)
	if e != nil {
		return e
	}
	if !allowed {
		return codingdraft.ErrConflict
	}
	return nil
}
func (s *Store) PrepareCodingDraft(ctx context.Context, remote codingdraft.Remote, ref string, t codingdraft.Target) (codingdraft.Receipt, error) {
	p, _, e := s.draftPlan(ctx, remote, ref, t)
	if e != nil {
		return codingdraft.Receipt{}, e
	}
	id, raw := draftDigest(p)
	e = s.Transaction(ctx, func(tx *sql.Tx) error {
		if e := s.codingDraftFence(ctx, tx, p); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO coding_draft_publication(approval,task_id,plan_json,status) SELECT ?,task_id,?,'planned' FROM task WHERE task_ref=? ON CONFLICT(approval) DO NOTHING`, id, string(raw), ref)
		return e
	})
	if e != nil {
		return codingdraft.Receipt{}, e
	}
	return s.CodingDraft(ctx, ref, id)
}
func (s *Store) CodingDraft(ctx context.Context, ref, id string) (codingdraft.Receipt, error) {
	v := codingdraft.Receipt{Approval: id}
	if !codingdraft.ValidDigest(id) {
		return v, codingdraft.ErrInvalid
	}
	var raw string
	e := s.db.QueryRowContext(ctx, `SELECT d.plan_json,d.status,d.head,d.number,d.url FROM coding_draft_publication d JOIN task t USING(task_id)
	WHERE d.approval=? AND t.task_ref=? AND t.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND t.created_at>coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=t.agent_id),0)`, id, ref).Scan(&raw, &v.Status, &v.Head, &v.Number, &v.URL)
	if e != nil {
		return v, e
	}
	if json.Unmarshal([]byte(raw), &v.Plan) != nil {
		return v, codingdraft.ErrInvalid
	}
	h, _ := draftDigest(v.Plan)
	if h != id || v.Plan.Task != ref {
		return v, codingdraft.ErrInvalid
	}
	return v, nil
}
func (s *Store) checkedDraftWrite(ctx context.Context, p codingdraft.Plan, query string, args ...any) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := s.codingDraftFence(ctx, tx, p); err != nil {
			return err
		}
		if query == "" {
			return nil
		}
		res, e := tx.ExecContext(ctx, query, args...)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return codingdraft.ErrConflict
		}
		return nil
	})
}
func (s *Store) ApproveCodingDraft(ctx context.Context, remote codingdraft.Remote, ref, id string) (codingdraft.Receipt, error) {
	v, e := s.CodingDraft(ctx, ref, id)
	if e != nil {
		return v, e
	}
	if v.Status != "planned" {
		return v, nil
	}
	p, tree, e := s.draftPlan(ctx, remote, ref, v.Plan.Target)
	if e != nil {
		return v, e
	}
	fresh, _ := draftDigest(p)
	if fresh != id {
		return v, codingdraft.ErrConflict
	}
	if e = s.checkedDraftWrite(ctx, p, "UPDATE coding_draft_publication SET status='started' WHERE approval=? AND status='planned'", id); e != nil {
		return v, e
	}
	// The durable claim is never resent. A process crash in any following
	// phase leaves started/unknown for the explicitly read-only reconciliation.
	e = s.publishCodingDraft(ctx, remote, p, tree, id)
	if e != nil {
		_, markErr := s.db.ExecContext(context.WithoutCancel(ctx), "UPDATE coding_draft_publication SET status='unknown' WHERE approval=? AND status='started'", id)
		if markErr != nil {
			return v, markErr
		}
	}
	current, loadErr := s.CodingDraft(ctx, ref, id)
	if loadErr != nil {
		return codingdraft.Receipt{}, loadErr
	}
	return current, nil
}
func (s *Store) publishCodingDraft(ctx context.Context, remote codingdraft.Remote, p codingdraft.Plan, tree coding.DraftTree, id string) error {
	fence := func() error { return s.checkedDraftWrite(ctx, p, "") }
	uploaded := map[string]bool{}
	for _, change := range p.Changes {
		if change.After == "" || change.After == change.Before || uploaded[change.After] {
			continue
		}
		if e := fence(); e != nil {
			return e
		}
		if e := remote.Blob(ctx, p.Target, tree.Blobs[change.After], change.After); e != nil {
			return e
		}
		uploaded[change.After] = true
	}
	if e := fence(); e != nil {
		return e
	}
	if e := remote.Tree(ctx, p.Target, tree.Entries, p.Tree); e != nil {
		return e
	}
	if e := fence(); e != nil {
		return e
	}
	head, e := remote.Commit(ctx, p)
	if e != nil {
		return e
	}
	if !codingdraft.ValidHead(head) {
		return codingdraft.ErrUnknown
	}
	if e = s.checkedDraftWrite(ctx, p, "UPDATE coding_draft_publication SET head=? WHERE approval=? AND status='started' AND head=''", head, id); e != nil {
		return e
	}
	target, e := remote.Inspect(ctx, p.Target)
	if e != nil || target.RepositoryID != p.RepositoryID || target.Base != p.BaseSHA || target.Branch != "" {
		return codingdraft.ErrConflict
	}
	if e = fence(); e != nil {
		return e
	}
	if e = remote.CreateRef(ctx, p.Target, head); e != nil {
		return e
	}
	target, e = remote.Inspect(ctx, p.Target)
	if e != nil || target.RepositoryID != p.RepositoryID || target.Base != p.BaseSHA || target.Branch != head {
		return codingdraft.ErrConflict
	}
	if e = fence(); e != nil {
		return e
	}
	pr, e := remote.CreatePull(ctx, p, id, head)
	if e != nil {
		return e
	}
	return s.saveCodingPull(ctx, p, id, head, pr)
}
func (s *Store) saveCodingPull(ctx context.Context, p codingdraft.Plan, id, head string, pr codingdraft.Pull) error {
	if pr.Head != head || pr.Base != p.Target.Base || pr.Number < 1 || pr.URL != "https://github.com/"+p.Target.Repository+"/pull/"+strconv.FormatInt(pr.Number, 10) {
		return codingdraft.ErrInvalid
	}
	return s.checkedDraftWrite(ctx, p, "UPDATE coding_draft_publication SET status='published',number=?,url=? WHERE approval=? AND head=? AND status IN ('started','unknown')", pr.Number, pr.URL, id, head)
}
func (s *Store) ReconcileCodingDraft(ctx context.Context, remote codingdraft.Remote, ref, id string) (codingdraft.Receipt, error) {
	v, e := s.CodingDraft(ctx, ref, id)
	if e != nil || v.Status == "published" || v.Status == "planned" || v.Head == "" {
		return v, e
	}
	if e = s.checkedDraftWrite(ctx, v.Plan, ""); e != nil {
		return v, e
	}
	pr, found, e := remote.FindPull(ctx, v.Plan, id, v.Head)
	if e != nil || !found {
		return v, e
	}
	if e = s.saveCodingPull(ctx, v.Plan, id, v.Head, pr); e != nil && !errors.Is(e, codingdraft.ErrConflict) {
		return v, e
	}
	return s.CodingDraft(ctx, ref, id)
}
