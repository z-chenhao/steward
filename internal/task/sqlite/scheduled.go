package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/automation"

	"github.com/z-chenhao/assistant/internal/task"
)

// ScheduledPage retains the configured ordering and a bounded result window.
// The continuation only names the owner's last visible row; private schedule
// times are never encoded in links for revoked configurations.
func (r *Reader) ScheduledPage(ctx context.Context, query, after string) ([]task.ScheduledWork, string, error) {
	if len(query) > 300 || len(after) > 256 {
		return nil, "", task.ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.automation_id,coalesce(t.task_ref,''),a.instruction,a.trigger_type,a.trigger_spec,a.timezone,a.version,a.enabled,a.next_fire_at,a.below_value,a.created_at,a.updated_at,a.owner_frequency_json,a.owner_local_json,a.presentation_json,
 EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE g.revision=a.method_graph AND json_extract(x.value,'$.contract')='steward.schedule-display/1'),
 EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE g.revision=a.method_graph AND json_extract(x.value,'$.contract')='steward.schedule-editor/1'),
 EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE g.revision=a.method_graph AND json_extract(x.value,'$.contract')='steward.schedule-defaults/1'),
 a.intent_privacy_epoch=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=a.agent_id),0)
 FROM automation a LEFT JOIN task t ON t.task_id=a.task_id AND t.agent_id=a.agent_id
 WHERE a.automation_id NOT LIKE 'trial:%' AND a.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1)
 AND (?='' OR (a.intent_privacy_epoch=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=a.agent_id),0) AND instr(lower(a.instruction),lower(?))>0))
 AND (?='' OR (-a.enabled,a.next_fire_at IS NULL,coalesce(a.next_fire_at,0),a.created_at,a.automation_id) >
 (SELECT -b.enabled,b.next_fire_at IS NULL,coalesce(b.next_fire_at,0),b.created_at,b.automation_id FROM automation b WHERE b.agent_id=a.agent_id AND b.automation_id=?))
 ORDER BY a.enabled DESC,a.next_fire_at IS NULL,a.next_fire_at,a.created_at,a.automation_id LIMIT 51`, query, query, after, after)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []task.ScheduledWork{}
	for rows.Next() {
		if len(out) == 50 {
			return out, out[len(out)-1].Ref, nil
		}
		v, e := scanScheduled(rows)
		if e != nil {
			return nil, "", e
		}
		out = append(out, v)
	}
	return out, "", rows.Err()
}

func scanScheduled(row scanner) (task.ScheduledWork, error) {
	var v task.ScheduledWork
	var next, below sql.NullInt64
	var frequency, local, presentation sql.NullString
	var visible, editorManaged, displayManaged, defaultsManaged bool
	var created, updated int64
	if err := row.Scan(&v.Ref, &v.TaskRef, &v.Instruction, &v.Type, &v.Spec, &v.Timezone, &v.Version, &v.Enabled, &next, &below, &created, &updated, &frequency, &local, &presentation, &displayManaged, &editorManaged, &defaultsManaged, &visible); err != nil {
		return v, err
	}
	v.CreatedAt = stamp(created)
	v.UpdatedAt = stamp(updated)
	if !visible {
		v.Schedule = task.Schedule{Ref: v.Ref, Version: v.Version, Enabled: v.Enabled, Restricted: true, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	} else {
		var err error
		v.Presentation, err = decodeSchedulePresentation(presentation, displayManaged, v.Type)
		if err != nil {
			return v, err
		}
		if defaultsManaged && (v.Presentation == nil || v.Presentation.EditorDefaults == nil || v.Presentation.EditorDefaults.Validate() != nil) {
			return v, task.ErrInvalid
		}
		if local.Valid {
			var facts automation.OwnerLocal
			if err := json.Unmarshal([]byte(local.String), &facts); err != nil {
				return v, err
			}
			v.OwnerLocal = &facts
		}
		if frequency.Valid {
			var facts automation.OwnerFrequency
			if err := json.Unmarshal([]byte(frequency.String), &facts); err != nil {
				return v, err
			}
			v.OwnerFrequency = &facts
		}
		if editorManaged && ((v.Type == "at" && v.OwnerLocal == nil) || (v.Type == "cron" && v.OwnerFrequency == nil)) {
			return v, task.ErrInvalid
		}
		if next.Valid {
			v.NextAt = stamp(next.Int64)
		}
		if below.Valid {
			value := below.Int64
			v.Below = &value
		}
	}
	return v, nil
}

// ScheduledDetail reads an owner-owned schedule even when it is outside the
// bounded list. Revoked context remains unavailable to the editor.
func (r *Reader) ScheduledDetail(ctx context.Context, ref string) (task.ScheduledWork, error) {
	if len(ref) > 256 {
		return task.ScheduledWork{}, task.ErrInvalid
	}
	v, err := scanScheduled(r.db.QueryRowContext(ctx, `SELECT a.automation_id,coalesce(t.task_ref,''),a.instruction,a.trigger_type,a.trigger_spec,a.timezone,a.version,a.enabled,a.next_fire_at,a.below_value,a.created_at,a.updated_at,a.owner_frequency_json,a.owner_local_json,a.presentation_json,
 EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE g.revision=a.method_graph AND json_extract(x.value,'$.contract')='steward.schedule-display/1'),
 EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE g.revision=a.method_graph AND json_extract(x.value,'$.contract')='steward.schedule-editor/1'),
 EXISTS(SELECT 1 FROM rsi_graph g,json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE g.revision=a.method_graph AND json_extract(x.value,'$.contract')='steward.schedule-defaults/1'),
 a.intent_privacy_epoch=coalesce((SELECT epoch FROM memory_privacy WHERE agent_id=a.agent_id),0)
 FROM automation a LEFT JOIN task t ON t.task_id=a.task_id AND t.agent_id=a.agent_id
 WHERE a.automation_id NOT LIKE 'trial:%' AND a.agent_id=(SELECT agent_id FROM agent_config WHERE singleton=1) AND a.automation_id=?`, ref))
	if err == sql.ErrNoRows {
		return v, task.ErrNotFound
	}
	return v, err
}

func decodeSchedulePresentation(raw sql.NullString, required bool, kind string) (*automation.SchedulePresentation, error) {
	if !raw.Valid {
		if required {
			return nil, task.ErrInvalid
		}
		return nil, nil
	}
	var value automation.SchedulePresentation
	if err := json.Unmarshal([]byte(raw.String), &value); err != nil {
		return nil, err
	}
	if value.FrequencyEnglish == "" || value.FrequencyChinese == "" || (kind == "at" && (value.TimeEnglish == "" || value.TimeChinese == "")) {
		return nil, task.ErrInvalid
	}
	return &value, nil
}
