package sqlite

import (
	"context"
	"encoding/json"
)

// StaticWebProjectionVersions identifies immutable delivery projections, or
// their absence, in page order. Any widget or non-passive published component
// requires the normal live projection and response-authority checks.
func (s *Store) StaticWebProjectionVersions(ctx context.Context, ids []string) ([]string, bool, error) {
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, false, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT coalesce(p.delivery_id,''), EXISTS(
	  SELECT 1 FROM ui_widget w WHERE w.message_id=m.value)
	  OR EXISTS(SELECT 1 FROM json_each(p.spec_json,'$.components') c
	     WHERE coalesce(json_extract(c.value,'$.type'),'') NOT IN ('markdown','artifact'))
	  FROM json_each(?) m LEFT JOIN delivery_request d ON d.message_id=m.value
	  LEFT JOIN delivery_projection p USING(delivery_id) ORDER BY m.key,d.delivery_id`, string(raw))
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	versions := make([]string, 0, len(ids))
	for rows.Next() {
		var v string
		var dynamic bool
		if err := rows.Scan(&v, &dynamic); err != nil {
			return nil, false, err
		}
		if dynamic {
			return nil, false, nil
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return versions, len(versions) == len(ids), nil
}
