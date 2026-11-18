package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type ownerSchedulePlan struct {
	agent, graph       string
	resolvedSpec       string
	defaults           bool
	label              bool
	display            bool
	presentation       *automation.SchedulePresentation
	project            bool
	projectedLocal     *automation.OwnerLocal
	projectedFrequency *automation.OwnerFrequency
	ref                string
	version            int64
	epoch              int64
	next               time.Time
	invalid            bool
	minimumBelow       int64
	managed            bool
	localDisable       bool
	requireActive      bool
	claimFence         func(context.Context, *sql.Tx) error
}

func ownerScheduleEditFence(ctx context.Context, tx *sql.Tx, plan ownerSchedulePlan) error {
	if plan.ref == "" {
		return nil
	}
	var version int64
	var eligible, taskActive bool
	err := tx.QueryRowContext(ctx, `SELECT a.version,a.intent_privacy_epoch=coalesce(p.epoch,0),a.task_id IS NULL OR t.status='active' FROM automation a LEFT JOIN memory_privacy p USING(agent_id) LEFT JOIN task t USING(task_id) WHERE a.automation_id=? AND a.agent_id=?`, plan.ref, plan.agent).Scan(&version, &eligible, &taskActive)
	if errors.Is(err, sql.ErrNoRows) {
		return agent.ErrConflict
	}
	if err != nil {
		return err
	}
	if version != plan.version || !eligible || (plan.requireActive && !taskActive) {
		return agent.ErrConflict
	}
	return nil
}

// Ever-activation is the boundary. Removing or disabling the service after its
// first accepted activation cannot recover the historical native implementation.
func scheduleActivatedTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var managed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation a JOIN rsi_graph g ON g.revision=json_extract(a.receipt_json,'$.graph_revision'),json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),json_each(p.manifest_json,'$.provides') x WHERE json_extract(x.value,'$.contract')='steward.schedule-next/1')`).Scan(&managed)
	return managed, err
}

func (r *Runs) ownerScheduleFence(ctx context.Context, tx *sql.Tx, plan ownerSchedulePlan) error {
	var owner, graph, ready string
	var epoch int64
	err := tx.QueryRowContext(ctx, `SELECT a.agent_id,a.setup_status,coalesce(c.active_graph,''),coalesce(p.epoch,0) FROM agent_config a JOIN rsi_control c ON c.singleton=1 LEFT JOIN memory_privacy p USING(agent_id) WHERE a.singleton=1`).Scan(&owner, &ready, &graph, &epoch)
	if err != nil {
		return err
	}
	if ready != "ready" {
		return agent.ErrSetup
	}
	if plan.claimFence != nil {
		if err := plan.claimFence(ctx, tx); err != nil {
			return err
		}
	}
	if owner != plan.agent || epoch != plan.epoch || (plan.claimFence == nil && graph != plan.graph) {
		return agent.ErrConflict
	}
	if err := ownerScheduleEditFence(ctx, tx, plan); err != nil {
		return err
	}
	if plan.managed {
		return checkScheduleGraphTx(ctx, tx, plan.graph)
	}
	return nil
}

// This is owner-side pure computation, outside the writer. The existing durable
// receipt is checked before calling a producer, so retry still works when that
// producer is later disabled or unavailable. The writer repeats that receipt
// check and fences the exact original owner/privacy/selection before acceptance.
func (r *Runs) prepareOwnerSchedule(ctx context.Context, key string, q automation.OwnerConfiguration, now time.Time) (ownerSchedulePlan, error) {
	raw, _ := encoded(q)
	return r.prepareOwnerScheduleRequest(ctx, "owner-config:"+key, digest("owner-configuration:"+raw), q, now, true)
}

func (r *Runs) prepareOwnerScheduleRequest(ctx context.Context, key, hash string, q automation.OwnerConfiguration, now time.Time, ownerEditor bool) (ownerSchedulePlan, error) {
	plan := ownerSchedulePlan{ref: q.Ref, version: q.Version, requireActive: q.Enabled}
	tx, err := r.host.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return plan, err
	}
	defer tx.Rollback()
	var ready string
	err = tx.QueryRowContext(ctx, `SELECT a.agent_id,a.setup_status,coalesce(c.active_graph,''),coalesce(p.epoch,0) FROM agent_config a JOIN rsi_control c ON c.singleton=1 LEFT JOIN memory_privacy p USING(agent_id) WHERE a.singleton=1`).Scan(&plan.agent, &ready, &plan.graph, &plan.epoch)
	if err != nil {
		return plan, err
	}
	if ready != "ready" {
		return plan, agent.ErrSetup
	}
	var prior string
	err = tx.QueryRowContext(ctx, `SELECT request_hash FROM automation_request WHERE agent_id=? AND request_key=?`, plan.agent, key).Scan(&prior)
	if err == nil {
		if prior != hash && !(ownerEditor && historicalOwnerScheduleReceipt(q, prior)) {
			return plan, agent.ErrConflict
		}
		return plan, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return plan, err
	}
	if !q.Enabled && q.Ref != "" {
		// Unchanged local stop is a Host control, not a new business configuration.
		var originalSpec string
		var originalFrequency, originalLocal sql.NullString
		var unchanged bool
		err = tx.QueryRowContext(ctx, `SELECT instruction=? AND trigger_type=? AND timezone=? AND below_value IS ?,trigger_spec,owner_frequency_json,owner_local_json FROM automation WHERE automation_id=? AND agent_id=?`, q.Instruction, q.Type, q.Timezone, q.Below, q.Ref, plan.agent).Scan(&unchanged, &originalSpec, &originalFrequency, &originalLocal)
		if err == nil && unchanged {
			if q.Frequency != nil {
				raw, _ := encoded(q.Frequency)
				if originalFrequency.Valid {
					plan.localDisable = raw == originalFrequency.String
				} else {
					legacy, legacyErr := automation.HistoricalFrequency(q.Frequency.Frequency, q.Frequency.Clock, q.Frequency.Weekday, q.Frequency.Day)
					plan.localDisable = legacyErr == nil && legacy == originalSpec
				}
			} else if q.LocalDate != "" {
				raw, _ := encoded(automation.OwnerLocal{Date: q.LocalDate, Clock: q.LocalClock})
				if originalLocal.Valid {
					plan.localDisable = raw == originalLocal.String
				} else {
					plan.localDisable = automation.HistoricalLocalMatch(originalSpec, q.Timezone, q.LocalDate, q.LocalClock)
				}
				if q.PreserveSpec != "" && q.PreserveSpec != originalSpec {
					return plan, agent.ErrConflict
				}
			} else {
				plan.localDisable = q.Spec == originalSpec
			}
			if plan.localDisable {
				plan.resolvedSpec = originalSpec
			}
		}

		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return plan, err
		}
		if plan.localDisable {
			return plan, nil
		}
	}
	if q.PreserveSpec != "" {
		var spec, zone string
		if err = tx.QueryRowContext(ctx, `SELECT trigger_spec,timezone FROM automation WHERE automation_id=? AND agent_id=?`, q.Ref, plan.agent).Scan(&spec, &zone); err != nil {
			return plan, err
		}
		if spec != q.PreserveSpec || zone != q.Timezone {
			return plan, agent.ErrConflict
		}
	}
	plan.managed, err = scheduleActivatedTx(ctx, tx)
	if err != nil {
		return plan, err
	}
	plan.defaults = plan.managed
	plan.label = plan.managed
	plan.display = plan.managed
	plan.project = plan.managed
	if plan.managed || ownerEditor {
		if err = ownerScheduleEditFence(ctx, tx, plan); err != nil {
			return plan, err
		}
	}
	if err = tx.Rollback(); err != nil {
		return plan, err
	}
	if !plan.managed {
		if q.Frequency != nil {
			q.Spec, err = automation.HistoricalFrequency(q.Frequency.Frequency, q.Frequency.Clock, q.Frequency.Weekday, q.Frequency.Day)
			if err != nil {
				plan.invalid = true
				return plan, nil
			}
			plan.resolvedSpec = q.Spec
		}
		if q.LocalDate != "" {
			if q.PreserveSpec != "" && automation.HistoricalLocalMatch(q.PreserveSpec, q.Timezone, q.LocalDate, q.LocalClock) {
				q.Spec = q.PreserveSpec
			} else {
				q.Spec, err = automation.HistoricalLocalTime(q.LocalDate, q.LocalClock, q.Timezone)
			}
			if err != nil {
				plan.invalid = true
				return plan, nil
			}
			plan.resolvedSpec = q.Spec
		}
		plan.next, err = automation.HistoricalNext(q.Type, q.Spec, q.Timezone, now)
		plan.invalid = err != nil
		return plan, nil
	}
	return r.computeSchedule(ctx, plan, q, now)
}

func (r *Runs) computeSchedule(ctx context.Context, plan ownerSchedulePlan, q automation.OwnerConfiguration, now time.Time) (ownerSchedulePlan, error) {
	catalog := rsi.NewCatalog(r.host.DataRoot())
	if configured := r.host.pluginCatalog.Load(); configured != nil {
		catalog = *configured
	}
	control := r.host.Plugins()
	frozen := plan
	control.Fence = func(ctx context.Context, tx *sql.Tx) error { return r.ownerScheduleFence(ctx, tx, frozen) }
	host := rsi.Host{Catalog: catalog, Store: control}
	if q.LocalDate != "" {
		input, _ := json.Marshal(contract.LocalTimeFacts{Date: q.LocalDate, Clock: q.LocalClock, Timezone: q.Timezone, OriginalSpec: q.PreserveSpec})
		output, err := host.ReadService(ctx, plan.graph, contract.LocalTime, input, nil)
		if err != nil {
			return plan, err
		}
		var local contract.LocalTimeResult
		if err = rsiplugin.DecodeResult(output, &local); err != nil {
			return plan, rsi.ErrInvalid
		}
		if local.ErrorCode == "INVALID_ARGUMENT" && local.Spec == "" {
			plan.invalid = true
			return plan, nil
		}
		at, err := time.Parse(time.RFC3339Nano, local.Spec)
		if local.ErrorCode != "" || err != nil || at.IsZero() || (at.UTC().Format(time.RFC3339) != local.Spec && local.Spec != q.PreserveSpec) {
			return plan, rsi.ErrInvalid
		}
		q.Spec = local.Spec
		plan.resolvedSpec = local.Spec
	}
	if q.Frequency != nil {
		facts := contract.FrequencyFacts{Frequency: q.Frequency.Frequency, Clock: q.Frequency.Clock, Weekday: q.Frequency.Weekday, Day: q.Frequency.Day}
		input, _ := json.Marshal(facts)
		output, err := host.ReadService(ctx, plan.graph, contract.Frequency, input, nil)
		if err != nil {
			return plan, err
		}
		var value contract.FrequencyResult
		if err = rsiplugin.DecodeResult(output, &value); err != nil {
			return plan, rsi.ErrInvalid
		}
		if value.ErrorCode == "INVALID_ARGUMENT" && value.Spec == "" {
			plan.invalid = true
			return plan, nil
		}
		if value.ErrorCode != "" || value.Spec == "" || len(value.Spec) > 256 {
			return plan, rsi.ErrInvalid
		}
		q.Spec = value.Spec
		plan.resolvedSpec = value.Spec
	}
	input, _ := json.Marshal(contract.NextFacts{Type: q.Type, Spec: q.Spec, Timezone: q.Timezone, After: now})
	output, err := host.ReadService(ctx, plan.graph, contract.Next, input, nil)
	if err != nil {
		return plan, err
	}
	var value contract.NextResult
	if err = rsiplugin.DecodeResult(output, &value); err != nil {
		return plan, rsi.ErrInvalid
	}
	if value.ErrorCode == "INVALID_ARGUMENT" && value.Next == "" {
		plan.invalid = true
		return plan, nil
	}
	if value.ErrorCode != "" {
		return plan, rsi.ErrInvalid
	}
	plan.next, err = time.Parse(time.RFC3339Nano, value.Next)
	if err != nil || plan.next.IsZero() || plan.next.UTC().Format(time.RFC3339Nano) != value.Next {
		return plan, rsi.ErrInvalid
	}
	if plan.project && q.LocalDate == "" && q.Frequency == nil {
		input, _ := json.Marshal(contract.ProjectionFacts{Type: q.Type, Spec: q.Spec, Timezone: q.Timezone})
		output, err := host.ReadService(ctx, plan.graph, contract.Projection, input, nil)
		if err != nil {
			return plan, err
		}
		var value contract.ProjectionResult
		if err = rsiplugin.DecodeResult(output, &value); err != nil || value.ErrorCode != "" {
			return plan, rsi.ErrInvalid
		}
		if q.Type == "at" {
			if value.Frequency != "once" || value.Date == "" || value.Clock == "" {
				return plan, rsi.ErrInvalid
			}
			plan.projectedLocal = &automation.OwnerLocal{Date: value.Date, Clock: value.Clock}
		} else {
			switch value.Frequency {
			case "daily", "weekdays", "weekly", "monthly", "custom":
			default:
				return plan, rsi.ErrInvalid
			}
			plan.projectedFrequency = &automation.OwnerFrequency{Frequency: value.Frequency, Clock: value.Clock, Weekday: value.Weekday, Day: value.Day}
		}
	}

	if plan.display {
		input, _ := json.Marshal(contract.DisplayFacts{Type: q.Type, Spec: q.Spec, Timezone: q.Timezone})
		output, err := host.ReadService(ctx, plan.graph, contract.Display, input, nil)
		if err != nil {
			return plan, err
		}
		var value contract.DisplayResult
		if err = rsiplugin.DecodeResult(output, &value); err != nil || value.ErrorCode != "" || value.FrequencyEnglish == "" || value.FrequencyChinese == "" || (q.Type == "at" && (value.TimeEnglish == "" || value.TimeChinese == "")) {
			return plan, rsi.ErrInvalid
		}
		plan.presentation = &automation.SchedulePresentation{FrequencyEnglish: value.FrequencyEnglish, FrequencyChinese: value.FrequencyChinese, TimeEnglish: value.TimeEnglish, TimeChinese: value.TimeChinese}
	}

	if plan.label {
		if plan.presentation == nil {
			return plan, rsi.ErrInvalid
		}
		input, _ := json.Marshal(contract.InstructionLabelFacts{Text: q.Instruction})
		output, err := host.ReadService(ctx, plan.graph, contract.InstructionLabel, input, nil)
		if err != nil {
			return plan, err
		}
		var value contract.InstructionLabelResult
		if err = rsiplugin.DecodeResult(output, &value); err != nil || !validScheduleInstructionLabel(value.Label) {
			return plan, rsi.ErrInvalid
		}
		plan.presentation.InstructionLabel = value.Label
	}

	if plan.defaults {
		if plan.presentation == nil {
			return plan, rsi.ErrInvalid
		}
		value, err := readScheduleDefaults(ctx, host, plan.graph)
		if err != nil {
			return plan, err
		}
		if q.Below != nil && *q.Below < value.MinBelow {
			plan.invalid = true
			plan.minimumBelow = value.MinBelow
			return plan, nil
		}
		plan.presentation.EditorDefaults = &value
	}

	return plan, nil
}

func checkScheduleGraphTx(ctx context.Context, tx *sql.Tx, graph string) error {
	var accepted, revoked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_activation WHERE json_extract(receipt_json,'$.graph_revision')=?),(SELECT stopped FROM rsi_control WHERE singleton=1) OR EXISTS(SELECT 1 FROM rsi_quarantine WHERE revision=?)`, graph, graph).Scan(&accepted, &revoked); err != nil {
		return err
	}
	if !accepted {
		return rsi.ErrNotFound
	}
	if revoked {
		return rsi.ErrRevoked
	}
	return nil
}

// Old Web forms hashed the already-converted instant. This interpreter only
// authenticates an existing receipt; it never accepts new managed work or
// changes its saved result. Retire with pre-local-input owner receipts.
func historicalOwnerScheduleReceipt(q automation.OwnerConfiguration, hash string) bool {
	var spec string
	var err error
	if q.Frequency != nil {
		spec, err = automation.HistoricalFrequency(q.Frequency.Frequency, q.Frequency.Clock, q.Frequency.Weekday, q.Frequency.Day)
		q.Frequency = nil
	} else if q.LocalDate != "" {
		if q.PreserveSpec != "" && automation.HistoricalLocalMatch(q.PreserveSpec, q.Timezone, q.LocalDate, q.LocalClock) {
			spec = q.PreserveSpec
		} else {
			q.PreserveSpec = ""
			spec, err = automation.HistoricalLocalTime(q.LocalDate, q.LocalClock, q.Timezone)
		}
	} else {
		return false
	}
	if err != nil {
		return false
	}
	q.Spec, q.LocalDate, q.LocalClock = spec, "", ""
	raw, _ := encoded(q)
	return digest("owner-configuration:"+raw) == hash
}

// The existing list field is bounded plain text; wording/whitespace belongs to
// its owning method, independently from the Task-name normalization policy.
func validScheduleInstructionLabel(label string) bool {
	return utf8.ValidString(label) && strings.TrimSpace(label) != "" && utf8.RuneCountInString(label) <= 80
}
