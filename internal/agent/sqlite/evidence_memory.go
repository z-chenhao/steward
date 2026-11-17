package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/evidence"
	"github.com/z-chenhao/assistant/internal/memory"
	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
	"github.com/z-chenhao/assistant/internal/recall"
)

var errMemoryEvidenceUnavailable = errors.New("memory evidence unavailable")

// ReadMemoryEvidence is only reached through local owner control. Plaintext
// stays outside the Tool pipeline and its durable results and checkpoints.
func (r *Runs) ReadMemoryEvidence(ctx context.Context, q evidence.Request, store *memorysqlite.Store, vault memory.Vault) (evidence.Page, error) {
	id, ok := strings.CutPrefix(q.Ref, "memory:")
	raw, err := encoded(q)
	if !ok || err != nil {
		return evidence.Page{}, evidence.ErrInvalid
	}
	if _, err = evidence.ParseOwner(raw); err != nil {
		return evidence.Page{}, err
	}
	ownerAccess := func() (a evidenceAccess, err error) {
		err = r.evidenceReadTx(ctx, func(tx *sql.Tx) error {
			var e error
			a, e = r.evidenceAccess(ctx, tx, nil)
			return e
		})
		return
	}
	access, err := ownerAccess()
	if err != nil {
		return evidence.Page{}, err
	}
	scope := recall.Scope{UserID: access.scope.UserID, AgentID: access.scope.AgentID}
	record, err := store.ReadEvidence(ctx, scope, id, access.floor, q.LinksOffset, vault)
	if err != nil {
		return evidence.Page{}, memoryEvidenceError(err)
	}
	if q.PageRevision != "" {
		if !access.pagePlugin {
			return evidence.Page{}, evidence.ErrInvalid
		}
		access.pageGraph = q.PageRevision
	}
	page := evidence.Page{Ref: q.Ref, Kind: "memory", OccurredAt: record.CreatedAt, Links: []evidence.Link{}}
	if err = r.evidenceWindow(ctx, access, &page, record.Text, q); err != nil {
		return evidence.Page{}, err
	}
	refs := record.Refs
	take, next, err := r.evidenceLinkWindow(ctx, access, q, len(refs))
	if err != nil {
		return evidence.Page{}, err
	}
	page.NextLinksOffset = next
	refs = refs[:take]
	for _, ref := range refs {
		if !evidence.ValidRef(ref) {
			continue
		}
		visible, err := r.memoryEvidenceSourceVisible(ctx, access, scope, ref)
		if errors.Is(err, agent.ErrClaimLost) {
			return evidence.Page{}, artifact.ErrNotFound
		}
		if err != nil {
			return evidence.Page{}, evidenceReadError(err)
		}
		if visible {
			page.Links = append(page.Links, evidence.Link{Relation: "source", Ref: ref})
		}
	}
	r.host.boundary("after_evidence_read")
	if err = store.RecheckEvidence(ctx, record); err != nil {
		return evidence.Page{}, memoryEvidenceError(err)
	}
	current, err := ownerAccess()
	if err != nil {
		return evidence.Page{}, err
	}
	if current.scope != access.scope || current.epoch != access.epoch || current.floor != access.floor {
		return evidence.Page{}, artifact.ErrNotFound
	}
	return page, nil
}

func memoryEvidenceError(err error) error {
	if errors.Is(err, recall.ErrConflict) {
		return artifact.ErrNotFound
	}
	return errMemoryEvidenceUnavailable
}

func (r *Runs) memoryEvidenceSourceVisible(ctx context.Context, a evidenceAccess, scope recall.Scope, ref string) (bool, error) {
	id, conversationRef := strings.CutPrefix(ref, "conversation:")
	if !conversationRef {
		return r.evidenceLinkVisible(ctx, a, ref)
	}
	history, ok := r.history.(interface {
		EvidenceSourceVisible(context.Context, recall.Scope, string, string, int64) (bool, error)
	})
	if !ok {
		return false, nil
	}
	visible, err := history.EvidenceSourceVisible(ctx, scope, a.conversation, id, a.floor)
	if !errors.Is(err, conversation.ErrConflict) {
		return visible, err
	}
	message, err := r.evidenceIntent(ctx, a.conversation, id)
	if errors.Is(err, artifact.ErrNotFound) {
		return false, nil
	}
	return err == nil && message.CreatedAt.UnixMicro() > a.floor, err
}
