package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	sqlitedriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Recorder owns a bounded, lossy diagnostic mailbox and its own short-lived
// writes. The Runtime writer and read-only Inspector remain separate. Failure,
// queue overflow or shutdown may leave gaps, which readers must show as unknown.
type Recorder struct {
	mu     sync.Mutex
	closed bool
	queue  chan llm.Dispatch
	done   chan struct{}
	db     *sql.DB
}

func NewRecorder(path string) *Recorder {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "busy_timeout(150)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	r := &Recorder{db: db, queue: make(chan llm.Dispatch, 32), done: make(chan struct{})}
	go r.run()
	return r
}

func (r *Recorder) Record(req llm.GenerationRequest, d llm.Dispatch) {
	if r == nil || len(d.Body) > 256<<10 || req.OwnerToken == "" || req.GenerationID != d.GenerationID {
		return
	} // probes are not business Runs
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	d.Body = bytes.Clone(d.Body)
	select {
	case r.queue <- d:
	default:
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	for d := range r.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		r.write(ctx, d)
		cancel()
	}
}

func (r *Recorder) write(ctx context.Context, d llm.Dispatch) {
	var body any
	if d.Body != nil {
		body = string(d.Body)
	}
	for {
		_, err := r.db.ExecContext(ctx, `INSERT INTO dispatch_observation VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, d.GenerationID, d.Attempt, d.Phase, d.At, d.Format, body, d.BodyHash, d.Omitted, d.HTTPStatus, d.Error)
		var sqliteErr *sqlitedriver.Error
		if err == nil || ctx.Err() != nil || !errors.As(err, &sqliteErr) || (sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY && sqliteErr.Code()&0xff != sqlite3.SQLITE_LOCKED) {
			return
		}
		// Retry only this idempotent diagnostic insert within its original budget.
		// A short domain write should not discard an otherwise available attempt.
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()
	<-r.done
	return r.db.Close()
}
