package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	artifactsqlite "github.com/z-chenhao/assistant/internal/artifact/sqlite"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/media/docx"
	webextract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent/parserresource"
)

type evidenceAccess struct {
	scope        artifact.Scope
	conversation string
	githubScope  string
	pdfText      bool
	docxText     bool
	docxPlugin   bool
	pluginGraph  string
	pagePlugin   bool
	pageGraph    string
	epoch, floor int64
	claim        agent.RunClaim
}

// The state pool defaults to immediate transactions for writes. ReadOnly makes
// this driver's BEGIN deferred, so evidence pages do not reserve the WAL writer.
func (r *Runs) evidenceReadTx(ctx context.Context, read func(*sql.Tx) error) error {
	tx, err := r.host.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return evidenceReadError(err)
	}
	defer tx.Rollback()
	return evidenceReadError(read(tx))
}

func (r *Runs) evidenceAccess(ctx context.Context, tx *sql.Tx, c agent.RunClaim) (a evidenceAccess, err error) {
	a.scope.UserID = r.host.OwnerID()
	a.claim = c
	if c == nil {
		a.scope.Owner = true
		a.docxText = true
		a.pdfText = true
		err = tx.QueryRowContext(ctx, `SELECT agent_id FROM agent_config WHERE singleton=1`).Scan(&a.scope.AgentID)
		a.conversation = a.scope.AgentID
	} else {
		if err = fence(ctx, tx, c); err != nil {
			return
		}
		var kind string
		err = tx.QueryRowContext(ctx, `SELECT kind,agent_id,conversation_id,coalesce((SELECT json_extract(policy_json,'$.private_pdf_text') FROM run_context_policy WHERE run_id=run.run_id),0),coalesce((SELECT json_extract(policy_json,'$.docx_text') FROM run_context_policy WHERE run_id=run.run_id),0),coalesce((SELECT json_extract(policy_json,'$.docx_plugin') FROM run_context_policy WHERE run_id=run.run_id),0)
		 FROM run WHERE run_id=?`, c.RunID()).Scan(&kind, &a.scope.AgentID, &a.conversation, &a.pdfText, &a.docxText, &a.docxPlugin)
		if err == nil && kind != "main" {
			err = artifact.ErrNotFound
		}
		a.scope.RunID = c.RunID()
	}
	if err != nil {
		return
	}
	if r.plugins != nil {
		if c == nil {
			a.docxPlugin, err = managedDocx(ctx, tx)
			if err != nil {
				return
			}
			err = tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,'') FROM rsi_control WHERE singleton=1`).Scan(&a.pluginGraph)
		} else {
			err = tx.QueryRowContext(ctx, `SELECT coalesce(graph_revision,'') FROM rsi_run WHERE run_id=?`, c.RunID()).Scan(&a.pluginGraph)
		}
		if err != nil {
			return
		}
	}
	if c == nil {
		a.pagePlugin, err = managedService(ctx, tx, "steward.evidence-text-window/1")
		if err == nil {
			err = tx.QueryRowContext(ctx, `SELECT coalesce(active_graph,'') FROM rsi_control WHERE singleton=1`).Scan(&a.pageGraph)
		}
	} else {
		err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT graph_revision FROM rsi_run WHERE run_id=?),''),EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value='steward.evidence-text-window/1')`, c.RunID(), c.RunID()).Scan(&a.pageGraph, &a.pagePlugin)
	}
	if err != nil {
		return
	}
	err = tx.QueryRowContext(ctx, `SELECT coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=?),0),coalesce((SELECT updated_at FROM memory_privacy WHERE agent_id=?),0)`, a.scope.AgentID, a.scope.AgentID).Scan(&a.epoch, &a.floor)
	return
}

func (r *Runs) evidenceFence(ctx context.Context, tx *sql.Tx, a evidenceAccess) error {
	if a.claim != nil {
		if err := fence(ctx, tx, a.claim); err != nil {
			return err
		}
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=?),0)`, a.scope.AgentID).Scan(&epoch); err != nil {
		return err
	}
	if epoch != a.epoch {
		return agent.ErrClaimLost
	}
	return nil
}

// ReadEvidence is the explicit local-owner equivalent of the Main-only Tool.
// Scope and privacy state are never accepted from its request.
func (r *Runs) ReadEvidence(ctx context.Context, q evidence.Request) (evidence.Page, error) {
	raw, err := encoded(q)
	if err != nil {
		return evidence.Page{}, evidence.ErrInvalid
	}
	if q, err = evidence.ParseOwner(raw); err != nil {
		return evidence.Page{}, err
	}
	page, err := r.readEvidence(ctx, q, nil)
	return page, evidenceReadError(err)
}

func evidenceReadError(err error) error {
	if errors.Is(err, artifact.ErrIntegrity) {
		return artifact.ErrNotFound
	}
	if err == nil || errors.Is(err, evidence.ErrInvalid) || errors.Is(err, evidence.ErrUnavailable) || errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrLimit) || errors.Is(err, agent.ErrClaimLost) || errors.Is(err, agent.ErrStorage) {
		return err
	}
	return &agent.OperationError{Operation: "linked evidence read", Category: agent.ErrStorage, Cause: err}
}

func (t *tools) readLinkedEvidence(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	var q evidence.Request
	denied := false
	invalid := false
	err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		kind, name, args, err := t.acceptedBuiltin(ctx, tx, inv)
		if errors.Is(err, tool.ErrApprovalDenied) || err == nil && (kind != "main" || name != evidence.ToolName) {
			denied = true
			return nil
		}
		if err != nil {
			return err
		}
		q, err = evidence.Parse(args)
		if err != nil {
			invalid = true
		}
		return nil
	})
	if err != nil {
		return tool.Result{}, evidenceReadError(err)
	}
	if denied {
		return tool.Failure("PERMISSION_DENIED"), nil
	}
	if invalid {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	page, err := t.runs.readEvidence(ctx, q, t.claim)
	if errors.Is(err, evidence.ErrUnavailable) {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	if errors.Is(err, evidence.ErrInvalid) {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if errors.Is(err, artifact.ErrLimit) {
		return tool.Failure("RESOURCE_LIMIT"), nil
	}
	if errors.Is(err, artifact.ErrNotFound) || errors.Is(err, artifact.ErrIntegrity) || errors.Is(err, conversation.ErrConflict) {
		return tool.Failure("NOT_FOUND"), nil
	}
	if err != nil {
		return tool.Result{}, evidenceReadError(err)
	}
	for i := range page.Links {
		page.Links[i].Kind, _, _ = strings.Cut(page.Links[i].Ref, ":")
	}
	raw, err := encoded(page)
	return tool.Result{Text: raw}, err
}

func (r *Runs) readEvidence(ctx context.Context, q evidence.Request, c agent.RunClaim) (page evidence.Page, err error) {
	return r.readScopedEvidence(ctx, q, c, "")
}

func (r *Runs) readScopedEvidence(ctx context.Context, q evidence.Request, c agent.RunClaim, githubScope string) (page evidence.Page, err error) {
	var access evidenceAccess
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		access, err = r.evidenceAccess(ctx, tx, c)
		if err != nil {
			return err
		}
		if c == nil {
			access.githubScope = githubScope
		}
		if q.Ref == "current" {
			var id string
			query := `SELECT message_id FROM main_input WHERE agent_id=? ORDER BY sequence DESC LIMIT 1`
			args := []any{access.scope.AgentID}
			if c != nil {
				query = `SELECT i.message_id FROM main_input i JOIN run r USING(run_id) WHERE i.run_id=? AND i.sequence<=coalesce(json_extract(r.checkpoint_json,'$.input_sequence'),0) ORDER BY i.sequence DESC LIMIT 1`
				args = []any{c.RunID()}
			}
			if err = tx.QueryRowContext(ctx, query, args...).Scan(&id); err == sql.ErrNoRows {
				return artifact.ErrNotFound
			} else if err != nil {
				return err
			}
			q.Ref = "conversation:" + id
		}
		return nil
	})
	if err != nil {
		return
	}
	if q.ReaderRevision != "" {
		if c != nil || (!access.docxPlugin && !access.pdfText) {
			return evidence.Page{}, evidence.ErrInvalid
		}
		access.pluginGraph = q.ReaderRevision
	}
	if q.PageRevision != "" {
		if c != nil || !access.pagePlugin {
			return evidence.Page{}, evidence.ErrInvalid
		}
		access.pageGraph = q.PageRevision
	}
	page = evidence.Page{Ref: q.Ref, Links: []evidence.Link{}}
	if access.pagePlugin {
		page.PageRevision = access.pageGraph
	}
	var parts []conversation.ContentPart
	switch {
	case strings.HasPrefix(q.Ref, "conversation:"):
		var message conversation.Message
		message, err = r.evidenceMessage(ctx, access, strings.TrimPrefix(q.Ref, "conversation:"))
		if err != nil {
			return
		}
		page.Kind = "conversation"
		page.Author = message.Role
		page.OccurredAt = message.CreatedAt
		parts = message.Content
		var text []string
		for _, part := range parts {
			if part.Type == "text" {
				text = append(text, part.Text)
			}
		}
		err = r.evidenceWindow(ctx, access, &page, strings.Join(text, "\n"), q)
	case strings.HasPrefix(q.Ref, "task:"):
		var text string
		var at int64
		err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
			if err := r.evidenceFence(ctx, tx, access); err != nil {
				return err
			}
			err := tx.QueryRowContext(ctx, `SELECT title,anchor_task,created_at FROM task WHERE task_ref=? AND agent_id=? AND conversation_id=? AND created_at>?`, q.Ref, access.scope.AgentID, access.conversation, access.floor).Scan(&page.Title, &text, &at)
			if err == sql.ErrNoRows {
				return artifact.ErrNotFound
			}
			return err
		})
		if err == nil {
			page.Kind = "task"
			page.OccurredAt = time.UnixMicro(at).UTC()
			err = r.evidenceWindow(ctx, access, &page, text, q)
		}
	case strings.HasPrefix(q.Ref, "artifact:"):
		err = r.evidenceArtifact(ctx, access, q, &page)
	case strings.HasPrefix(q.Ref, "github:"):
		err = r.readGitHubObservation(ctx, access, q, &page)
	default:
		err = evidence.ErrInvalid
	}
	if err != nil {
		return
	}
	err = r.evidenceLinks(ctx, access, q, parts, &page)
	if err != nil {
		return evidence.Page{}, err
	}
	// Cross-store/file IO cannot keep a state transaction open. Recheck the
	// original epoch after the last read before any result is returned/frozen.
	r.host.boundary("after_evidence_read")
	err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := r.evidenceFence(ctx, tx, access); err != nil {
			return err
		}
		if page.Kind == "artifact" {
			_, err := r.host.Artifacts().DescribeTx(ctx, tx, access.scope, q.Ref, true)
			return err
		}
		return r.recheckGitHubEvidence(ctx, tx, access, &page)
	})
	if err != nil {
		return evidence.Page{}, err
	}
	return
}

func (r *Runs) evidenceMessage(ctx context.Context, a evidenceAccess, id string) (conversation.Message, error) {
	var m conversation.Message
	var err error
	if r.history != nil {
		m, err = r.history.Get(ctx, a.conversation, id)
	} else {
		err = conversation.ErrConflict
	}
	if errors.Is(err, conversation.ErrConflict) {
		// A newly accepted input may precede the recoverable history flush. Its
		// immutable intent is the same original, never a synthetic replacement.
		m, err = r.evidenceIntent(ctx, a.conversation, id)
	}
	if err != nil {
		return m, err
	}
	if m.ConversationID != a.conversation || m.CreatedAt.UnixMicro() <= a.floor {
		return conversation.Message{}, artifact.ErrNotFound
	}
	return m, nil
}

func (r *Runs) evidenceIntent(ctx context.Context, conversationID, id string) (m conversation.Message, err error) {
	var raw string
	err = r.host.db.QueryRowContext(ctx, `SELECT message_json FROM conversation_intent WHERE message_id=? AND json_extract(message_json,'$.conversation_id')=?`, id, conversationID).Scan(&raw)
	if err == sql.ErrNoRows {
		return m, artifact.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &m)
	}
	return
}

func (r *Runs) evidenceArtifact(ctx context.Context, a evidenceAccess, q evidence.Request, page *evidence.Page) error {
	authorize := func(tx *sql.Tx) (artifact.Scope, error) {
		if err := r.evidenceFence(ctx, tx, a); err != nil {
			return artifact.Scope{}, err
		}
		d, err := r.host.Artifacts().DescribeTx(ctx, tx, a.scope, q.Ref, true)
		if err == nil && d.CreatedAt.UnixMicro() <= a.floor {
			err = artifact.ErrNotFound
		}
		if err == nil && a.docxText && d.ByteSize > docx.MaxArchiveBytes {
			media, _, _ := mime.ParseMediaType(d.MediaType)
			if media == docx.MediaType {
				err = fmt.Errorf("%w: %s", artifact.ErrLimit, docx.ErrLimit)
			}
		}
		return a.scope, err
	}
	var opened *artifactsqlite.Opened
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		scope, err := authorize(tx)
		if err != nil {
			return err
		}
		opened, err = r.host.Artifacts().OpenFileTx(ctx, tx, scope, q.Ref)
		return err
	})
	if err != nil {
		return err
	}
	f, err := opened.Verify(ctx)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error { _, err := authorize(tx); return err }); err != nil {
		return err
	}
	d := opened.Descriptor()
	page.Kind = "artifact"
	page.Title = d.Name
	page.OccurredAt = d.CreatedAt
	page.Offset = q.Offset
	page.TotalBytes = d.ByteSize
	media, _, _ := mime.ParseMediaType(d.MediaType)
	if q.ReaderRevision != "" && !(media == docx.MediaType && a.docxPlugin || media == "application/pdf" && a.pdfText) {
		return evidence.ErrInvalid
	}
	if a.pdfText && media == "application/pdf" {
		if d.ByteSize > parserresource.MaxDocumentBytes {
			return artifact.ErrLimit
		}
		if r.plugins == nil || a.pluginGraph == "" {
			return evidence.ErrUnavailable
		}
		data, err := io.ReadAll(io.LimitReader(f, parserresource.MaxDocumentBytes+1))
		if err != nil {
			return err
		}
		if len(data) > parserresource.MaxDocumentBytes {
			return artifact.ErrLimit
		}
		check := func(c context.Context) error {
			return r.evidenceReadTx(c, func(tx *sql.Tx) error { _, err := authorize(tx); return err })
		}
		// The private bytes have no public URL. Only the frozen, isolated
		// pure parser may receive them after an original-source authorization.
		out, err := r.plugins.ExtractService(ctx, a.pluginGraph, webextract.Request{InputID: "document", MediaType: media}, data, check)
		if err != nil {
			if errors.Is(err, rsi.ErrNotFound) || errors.Is(err, rsi.ErrRevoked) || err.Error() == "WEB_EXTRACTOR_UNAVAILABLE" {
				return evidence.ErrUnavailable
			}
			if err.Error() == "WEB_EXTRACTION_FAILED" {
				return evidence.ErrInvalid
			}
			return err
		}
		if err = check(ctx); err != nil {
			return err
		}
		page.Representation = "pdf_text"
		if a.claim == nil {
			page.ReaderRevision = a.pluginGraph
		}
		return r.evidenceWindow(ctx, a, page, out.Text, q)
	}
	if a.docxText && media == docx.MediaType {
		text, err := r.docxText(ctx, a, q.Ref, f)
		switch {
		case errors.Is(err, docx.ErrInvalid):
			return fmt.Errorf("%w: %s", evidence.ErrInvalid, err)
		case errors.Is(err, docx.ErrLimit):
			return fmt.Errorf("%w: %s", artifact.ErrLimit, err)
		case err != nil:
			return err
		}
		page.Representation = "docx_text"
		if a.claim == nil && a.docxPlugin {
			page.ReaderRevision = a.pluginGraph
		}
		return r.evidenceWindow(ctx, a, page, text, q)
	}
	if a.claim == nil && wavMediaType(media) {
		enabled, err := r.localTranscriptionEnabled(ctx, a)
		if err != nil {
			return err
		}
		if enabled {
			page.LocalTranscriptionCommand = "audio transcribe-local"
		}
	}
	if !strings.HasPrefix(media, "text/") && media != "application/json" && !strings.HasSuffix(media, "+json") {
		if q.Offset != 0 {
			return evidence.ErrInvalid
		}
		return nil
	}
	if q.Offset > d.ByteSize {
		return evidence.ErrInvalid
	}
	if _, err = io.CopyN(io.Discard, f, q.Offset); err != nil {
		return err
	}
	limit := q.Limit
	if limit == 0 {
		limit = 4096
	}
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+3))
	if err != nil {
		return err
	}
	if len(b) > 0 && !utf8.RuneStart(b[0]) {
		return evidence.ErrInvalid
	}
	end := min(len(b), limit)
	for end > 0 && !utf8.Valid(b[:end]) {
		end--
	}
	if end == 0 && len(b) > 0 {
		return evidence.ErrInvalid
	}
	if a.pagePlugin {
		return r.evidenceTextChunk(ctx, a, page, string(b[:end]), d.ByteSize, q)
	}
	page.Text = string(b[:end])
	page.Bytes = end
	next := q.Offset + int64(end)
	if next < d.ByteSize {
		page.NextOffset = &next
	}
	return nil
}
