// Package sqlite stores event intent and consumer deduplication in the domain
// state.db. It does not open a second database or commit a caller's transaction.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/z-chenhao/assistant/internal/event"
)

type Store struct {
	db          *sql.DB
	MaxFailures int
	OnDead      func(string)
}

func New(db *sql.DB) *Store { return &Store{db: db} }

// Enqueue must use the very same transaction as the domain mutation. Duplicate
// event IDs fail closed, including when their parameters have changed.
func Enqueue(ctx context.Context, tx *sql.Tx, e event.CloudEvent) error {
	payload, err := event.Encode(e)
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO event_outbox
 (outbox_id, event_id, subject, type, payload_json, occurred_at, attempt_count)
 VALUES (?, ?, ?, ?, ?, ?, 0)`, id.String(), e.ID(), e.Subject(), e.Type(), string(payload), e.Time().UTC().UnixMicro())
	return err
}

func (s *Store) Pending(ctx context.Context, limit int) ([]event.CloudEvent, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("outbox batch must be between 1 and 1000")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id,payload_json FROM event_outbox WHERE published_at IS NULL AND dead_at IS NULL ORDER BY attempt_count,outbox_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []event.CloudEvent
	var poison []string
	for rows.Next() {
		var payload []byte
		var id string
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		e, err := event.Decode(payload)
		if err != nil || e.ID() != id {
			poison = append(poison, id)
			continue
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, id := range poison {
		result, e := s.db.ExecContext(ctx, `UPDATE event_outbox SET dead_at=?,failure_code='INVALID_ENVELOPE' WHERE event_id=? AND published_at IS NULL AND dead_at IS NULL`, time.Now().UnixMicro(), id)
		if e != nil {
			return nil, e
		}
		if n, _ := result.RowsAffected(); n > 0 && s.OnDead != nil {
			s.OnDead("EVENT_OUTBOX_INVALID")
		}
	}
	return events, nil
}

// Only non-transient publication errors count toward poison isolation. Broker
// downtime retains all intents, regardless of the number of reconnect attempts.
func (s *Store) RecordFailure(ctx context.Context, id string) error {
	limit := s.MaxFailures
	if limit == 0 {
		limit = 5
	}
	var dead int64
	err := s.db.QueryRowContext(ctx, `UPDATE event_outbox SET failure_count=failure_count+1,failure_code='PUBLICATION_REJECTED',dead_at=CASE WHEN failure_count+1>=? THEN ? ELSE dead_at END WHERE event_id=? AND published_at IS NULL AND dead_at IS NULL RETURNING coalesce(dead_at,0)`, limit, time.Now().UnixMicro(), id).Scan(&dead)
	if err == sql.ErrNoRows {
		return nil
	}
	if err == nil && dead > 0 && s.OnDead != nil {
		s.OnDead("EVENT_OUTBOX_POISON")
	}
	return err
}

// Retry requeues only a validated internal wakeup. It cannot retry a Tool or
// Delivery side effect. The attempt count fences concurrent operator decisions.
func (s *Store) Retry(ctx context.Context, id string, attempts int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload_json FROM event_outbox WHERE event_id=? AND dead_at IS NOT NULL AND attempt_count=?`, id, attempts).Scan(&raw); err != nil {
		return err
	}
	e, err := event.Decode(raw)
	if err != nil || e.ID() != id {
		return event.ErrPoison
	}
	if _, err = tx.ExecContext(ctx, `UPDATE event_outbox SET dead_at=NULL,failure_code=NULL,attempt_count=0,failure_count=0 WHERE event_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordAttempt(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE event_outbox SET attempt_count = attempt_count + 1 WHERE event_id = ? AND published_at IS NULL`, id)
	return err
}

func (s *Store) MarkPublished(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE event_outbox SET published_at = ?,dead_at=NULL,failure_code=NULL WHERE event_id = ? AND published_at IS NULL`, time.Now().UTC().UnixMicro(), id)
	return err
}

// Effect performs SQL domain changes in this transaction only. No external
// effects or Store calls are permitted: the store has one connection. The domain
// owns version/epoch checks for late or out-of-order events.
type Effect func(context.Context, *sql.Tx, event.CloudEvent) error

// Handle commits the Inbox record and domain effect together, THEN acknowledges.
// handled reports whether this invocation committed a new effect. An ack error
// may accompany handled=true; redelivery will skip the committed effect.
func (s *Store) Handle(ctx context.Context, consumer string, delivery event.Delivery, effect Effect) (handled bool, err error) {
	if consumer == "" || effect == nil {
		return false, errors.New("consumer and transactional effect required")
	}
	e := delivery.Event()
	if err := event.Validate(e); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO event_inbox (consumer, event_id, processed_at) VALUES (?, ?, ?) ON CONFLICT (consumer, event_id) DO NOTHING`, consumer, e.ID(), time.Now().UTC().UnixMicro())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 1 {
		if err := effect(ctx, tx, e); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n == 1, delivery.Ack(ctx)
}
