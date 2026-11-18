package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/projectcontainer"
)

// Resource cleanup is a trusted recovery action. It cannot re-run a command or
// manufacture a Tool outcome; failed/unknown attempts retain their history.
func (r *Runs) recoverProjectResources(ctx context.Context) error {
	now := time.Now().UnixMicro()
	rows, err := r.host.db.QueryContext(ctx, `SELECT x.invocation_id,x.socket_path,x.directory,p.image_id,r.task_id,x.create_started,x.creation_observed FROM coding_project_resource x JOIN coding_project_plan p USING(invocation_id) JOIN tool_invocation i USING(invocation_id) JOIN run r USING(run_id) LEFT JOIN run_claim c USING(run_id) WHERE x.released=0 AND x.next_cleanup_at<=? AND NOT(i.status='running' AND i.desired_state='running' AND r.desired_state='running' AND c.token IS NOT NULL AND c.token=i.execution_token AND c.expires_at>? AND NOT coalesce((SELECT stopped FROM rsi_control WHERE singleton=1),0) AND NOT EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=p.graph_revision)) ORDER BY i.created_at LIMIT 8`, now, now)
	if err != nil {
		return err
	}
	type resource struct {
		invocation, socket, directory, image, task string
		started, observed                          bool
	}
	var pending []resource
	for rows.Next() {
		var v resource
		if err := rows.Scan(&v.invocation, &v.socket, &v.directory, &v.image, &v.task, &v.started, &v.observed); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range pending {
		if err := r.host.Transaction(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE coding_project_resource SET next_cleanup_at=? WHERE invocation_id=? AND released=0`, now+5_000_000, v.invocation)
			return err
		}); err != nil {
			return err
		}
		parent := filepath.Dir(coding.Location(r.host.codeRoot, v.task).Directory)
		if filepath.Dir(v.directory) != parent || !strings.HasPrefix(filepath.Base(v.directory), "candidate-") {
			continue
		}
		client, err := projectcontainer.New(projectcontainer.Config{Socket: v.socket, Image: v.image})
		if err != nil {
			continue
		}
		cleanup, cancel := context.WithTimeout(ctx, 2*time.Second)
		observed, removeErr := client.Remove(cleanup, v.invocation)
		cancel()
		client.Close()
		if removeErr != nil || v.started && !v.observed && !observed {
			continue
		}
		info, err := os.Lstat(v.directory)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if os.RemoveAll(v.directory) != nil {
				continue
			}
		} else if !os.IsNotExist(err) {
			continue
		}
		if err := r.host.Transaction(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE coding_project_resource SET released=1,creation_observed=max(creation_observed,?) WHERE invocation_id=?`, observed, v.invocation)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
