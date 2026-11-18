package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/computer/environment"
	"github.com/z-chenhao/assistant/internal/computer/projectcontainer"
)

// Compare actual inputs without changing retained receipts or claiming that an
// unchanged input proves the requirement. Unavailable facts remain unavailable.
func (t *tools) projectEnvironmentStatus(ctx context.Context, taskID string, cfg environment.Config, status *coding.CheckStatus) error {
	if cfg.ProjectContainer == nil {
		return nil
	}
	current := &coding.ProjectEnvironment{Image: cfg.ProjectContainer.Image}
	status.Environment = current
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	client, err := projectcontainer.New(*cfg.ProjectContainer)
	if err == nil {
		basis, basisErr := client.Basis(probe)
		client.Close()
		err = basisErr
		if err == nil {
			current.Runtime = &basis
			current.SHA256 = basis.Digest()
		}
	}
	cancel()
	if err != nil {
		current.ErrorCode = "PROVIDER_UNAVAILABLE"
	}
	var savedImage string
	if err := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error {
		if err := t.fence(ctx, tx); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT image_id,environment_sha256 FROM coding_task_environment WHERE task_id=?`, taskID).Scan(&savedImage, &current.TaskSHA256)
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}); err != nil {
		return err
	}
	if savedImage != "" && (savedImage != current.Image || current.SHA256 != "") {
		current.TaskMatches = new(savedImage == current.Image && current.SHA256 == current.TaskSHA256)
	}
	var revision string
	if t.runs.plugins != nil {
		h := t.runs.pluginHostWithFence(t.fence)
		var err error
		revision, err = h.Store.Binding(ctx, t.claim.RunID())
		if err != nil {
			return err
		}
	}
	driver := coding.ProjectDriverFingerprint(projectcontainer.DriverFingerprint())
	for i := range status.Checks {
		check := &status.Checks[i]
		if check.Project == nil {
			continue
		}
		if revision != "" {
			check.MethodMatches = new(check.MethodRevision == revision)
		}
		check.DriverMatches = new(check.DriverHash == driver)
		if check.Image != current.Image || current.SHA256 != "" {
			check.EnvironmentMatches = new(check.Image == current.Image && check.EnvironmentSHA256 == current.SHA256)
		}
	}
	return nil
}
