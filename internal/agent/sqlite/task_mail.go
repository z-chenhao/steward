package sqlite

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/plugin/taskmail/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type TaskMailbox struct {
	Address string `json:"address"`
	TaskRef string `json:"task_ref"`
}
type TaskMailReceipt struct {
	TaskRef     string `json:"task_ref"`
	ArtifactRef string `json:"artifact_ref"`
}

var mailDomainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var mailDeliveryID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@+-]{0,199}$`)

func validMailDomain(domain string) bool {
	if len(domain) > 190 || !strings.Contains(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if !mailDomainLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// BindTaskMailbox is an explicit installation-owner action. An address is
// anchored to the current Task epoch and privacy epoch, including on reopen.
func (s *Store) BindTaskMailbox(ctx context.Context, ref, domain string) (TaskMailbox, error) {
	domain = strings.ToLower(domain)
	out := TaskMailbox{TaskRef: ref}
	if !validMailDomain(domain) {
		return out, errors.New("invalid mail domain")
	}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := mailServiceAvailable(ctx, tx); err != nil {
			return err
		}
		var id string
		var epoch, privacy int64
		err := tx.QueryRowContext(ctx, `SELECT t.task_id,t.task_epoch,coalesce(p.epoch,0) FROM task t JOIN agent_config c ON c.agent_id=t.agent_id AND c.singleton=1 LEFT JOIN memory_privacy p ON p.agent_id=t.agent_id
		  WHERE t.task_ref=? AND t.status='active' AND t.created_at>coalesce(p.updated_at,0)`, ref).Scan(&id, &epoch, &privacy)
		if err == sql.ErrNoRows {
			return rsi.ErrRevoked
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE task_mailbox SET closed_at=? WHERE task_id=? AND closed_at IS NULL AND (task_epoch!=? OR privacy_epoch!=?)`, time.Now().UnixMicro(), id, epoch, privacy); err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `SELECT address FROM task_mailbox WHERE task_id=? AND closed_at IS NULL`, id).Scan(&out.Address)
		if err == nil {
			if !strings.HasSuffix(out.Address, "@"+domain) {
				return agent.ErrConflict
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		out.Address = "ghost-" + strings.ToLower(rand.Text()) + "@" + domain
		_, err = tx.ExecContext(ctx, `INSERT INTO task_mailbox(address,task_id,task_epoch,privacy_epoch,created_at) VALUES(?,?,?,?,?)`, out.Address, id, epoch, privacy, time.Now().UnixMicro())
		return err
	})
	return out, err
}

func (s *Store) CloseTaskMailbox(ctx context.Context, address string) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var closed sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT m.closed_at FROM task_mailbox m JOIN task t USING(task_id) JOIN agent_config c ON c.agent_id=t.agent_id WHERE c.singleton=1 AND m.address=?`, address).Scan(&closed); err != nil {
			if err == sql.ErrNoRows {
				return rsi.ErrNotFound
			}
			return err
		}
		if closed.Valid {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE task_mailbox SET closed_at=? WHERE address=? AND closed_at IS NULL`, time.Now().UnixMicro(), address)
		return err
	})
}

type taskMailAccess struct {
	agent, task, ref, graph string
	epoch, privacy          int64
}
type mailReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func mailboxAccess(ctx context.Context, db mailReader, address string) (taskMailAccess, error) {
	var a taskMailAccess
	err := db.QueryRowContext(ctx, `SELECT t.agent_id,t.task_id,t.task_ref,c.active_graph,t.task_epoch,m.privacy_epoch FROM task_mailbox m JOIN task t USING(task_id)
	 JOIN agent_config ac ON ac.agent_id=t.agent_id AND ac.singleton=1 JOIN rsi_control c ON c.singleton=1 LEFT JOIN memory_privacy p ON p.agent_id=t.agent_id
	 WHERE m.address=? AND m.closed_at IS NULL AND m.task_epoch=t.task_epoch AND t.status='active' AND m.privacy_epoch=coalesce(p.epoch,0) AND t.created_at>coalesce(p.updated_at,0)
	 AND c.stopped=0 AND c.active_graph IS NOT NULL AND NOT EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=c.active_graph)`, address).Scan(&a.agent, &a.task, &a.ref, &a.graph, &a.epoch, &a.privacy)
	if err == sql.ErrNoRows {
		err = rsi.ErrRevoked
	}
	return a, err
}
func mailReceipt(ctx context.Context, db mailReader, address, key, hash string) (string, error) {
	var ref, stored string
	err := db.QueryRowContext(ctx, `SELECT artifact_ref,content_hash FROM task_mail_receipt WHERE address=? AND message_key=?`, address, key).Scan(&ref, &stored)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err == nil && stored != hash {
		return "", agent.ErrConflict
	}
	return ref, err
}

func (s *Store) ReceiveTaskMail(ctx context.Context, address, deliveryID string, src io.Reader) (TaskMailReceipt, error) {
	var out TaskMailReceipt
	if !mailDeliveryID.MatchString(deliveryID) {
		return out, errors.New("invalid mail transport ID")
	}
	access, err := mailboxAccess(ctx, s.db, address)
	if err != nil {
		return out, err
	}
	out.TaskRef = access.ref
	fence := func(ctx context.Context, tx *sql.Tx) error {
		current, err := mailboxAccess(ctx, tx, address)
		if err != nil {
			return err
		}
		if current != access {
			return rsi.ErrRevoked
		}
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(src, contract.MaxBytes+1))
	if err != nil {
		return out, err
	}
	if len(raw) == 0 || len(raw) > contract.MaxBytes {
		return out, errors.New("mail transport size invalid")
	}
	key := rsiplugin.FileDigest([]byte(deliveryID))
	hash := rsiplugin.FileDigest(raw)
	if err = s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx); err != nil {
			return err
		}
		if err := mailServiceAvailable(ctx, tx); err != nil {
			return err
		}
		var e error
		out.ArtifactRef, e = mailReceipt(ctx, tx, address, key, hash)
		return e
	}); err != nil || out.ArtifactRef != "" {
		return out, err
	}
	control := s.Plugins()
	control.Fence = fence
	catalog := rsi.NewCatalog(s.DataRoot())
	if configured := s.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	h := rsi.Host{Catalog: catalog, Store: control}
	value, err := h.ReadService(ctx, access.graph, contract.Service, json.RawMessage(`{}`), []rsi.InputData{{ID: "message", Data: raw}})
	if err != nil {
		return out, err
	}
	var m contract.Message
	if rsiplugin.DecodeResult(value, &m) != nil || m.Error != "" || !utf8.ValidString(m.Text) || len(m.Text) > contract.MaxBytes || strings.ContainsRune(m.Text, 0) || len(m.Subject) > 998 || strings.ContainsAny(m.From+m.Subject+m.MessageID, "\x00\r\n") {
		return out, errors.New("mail format unsupported or invalid")
	}
	if _, err = mail.ParseAddress(m.From); err != nil {
		return out, errors.New("invalid mail sender")
	}
	// A text Artifact is evidence. It has no input, instruction or approval authority.
	text := fmt.Sprintf("From: %s\nSubject: %s\nMessage-ID: %s\n\n%s", m.From, m.Subject, m.MessageID, m.Text)
	input, err := s.PrepareArtifactInput(ctx, artifact.Input{Name: "forwarded-email.txt", MediaType: "text/plain"})
	if err != nil {
		return out, err
	}
	staged, err := s.Artifacts().Stage(ctx, input, bytes.NewBufferString(text))
	if err != nil {
		return out, err
	}
	defer staged.Close()
	s.boundary("before_task_mail_commit")
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx); err != nil {
			return err
		}
		ref, e := mailReceipt(ctx, tx, address, key, hash)
		if e != nil {
			return e
		}
		if ref != "" {
			out.ArtifactRef = ref
			return nil
		}
		scope := artifact.Scope{UserID: s.OwnerID(), AgentID: access.agent, TaskID: access.task, Owner: true}
		out.ArtifactRef, e = s.Artifacts().CommitTx(ctx, tx, scope, staged, "")
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO task_mail_receipt VALUES(?,?,?,?,?,?)`, address, key, hash, out.ArtifactRef, access.graph, time.Now().UnixMicro())
		return e
	})
	return out, err
}

// RecentTaskMail lists accepted evidence under the current user's Task and
// privacy boundary. Closing an address stops admission; existing references
// continue to use ordinary Artifact authorization.
func (s *Store) RecentTaskMail(ctx context.Context, ref string, before int64) ([]TaskMailItem, error) {
	if before < 0 {
		return nil, agent.ErrConflict
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.rowid,r.artifact_ref,r.received_at FROM task_mail_receipt r JOIN task_mailbox m USING(address) JOIN task t USING(task_id)
	JOIN agent_config c ON c.agent_id=t.agent_id AND c.singleton=1 LEFT JOIN memory_privacy p ON p.agent_id=t.agent_id JOIN artifact a ON a.artifact_ref=r.artifact_ref
	WHERE t.task_ref=? AND m.privacy_epoch=coalesce(p.epoch,0) AND t.created_at>coalesce(p.updated_at,0) AND a.deleted_at IS NULL AND (?=0 OR r.rowid<?) ORDER BY r.rowid DESC LIMIT 50`, ref, before, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaskMailItem{}
	for rows.Next() {
		var item TaskMailItem
		var at int64
		if err := rows.Scan(&item.Sequence, &item.ArtifactRef, &at); err != nil {
			return nil, err
		}
		item.ReceivedAt = time.UnixMicro(at).UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

type TaskMailItem struct {
	Sequence    int64     `json:"sequence"`
	ArtifactRef string    `json:"artifact_ref"`
	ReceivedAt  time.Time `json:"received_at"`
}
