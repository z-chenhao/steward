package github

import (
	"context"
	"time"
)

// readLegacyV1 serves actual pre-opt-in selections/in-flight poll tickets and
// direct native embedders. Managed polls never call it, including on failure.
// Delete when these named consumers have migrated; old accepted observations
// separately retain their frozen native followup preview interpretation.
func (c Client) readLegacyV1(ctx context.Context, cfg Config) (Batch, error) {
	r, err := c.reader(ctx, cfg)
	if err != nil {
		return Batch{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	batch, self, err := r.identity(ctx, cfg)
	if err != nil {
		return Batch{}, err
	}
	for _, kind := range []string{"review_comment", "review", "check_run"} {
		for page := 1; ; page++ {
			rows, err := r.page(ctx, cfg, batch.HeadSHA, self, kind, page)
			if err != nil {
				return Batch{}, err
			}
			for _, row := range rows {
				if err := row.validate(); err != nil {
					return Batch{}, err
				}
				if !row.eligible {
					continue
				}
				e, err := row.project(16<<10, "\n[truncated; open the source URL for full text]")
				if err != nil {
					return Batch{}, err
				}
				if err = batch.add(e); err != nil {
					return Batch{}, err
				}
			}
			if len(rows) < pageSize {
				break
			}
			if page == maxPages {
				return Batch{}, &Failure{"HISTORY_LIMIT"}
			}
		}
	}
	return batch, nil
}
