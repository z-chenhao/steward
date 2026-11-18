package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/task"
)

type ScheduleSettings interface {
	ScheduleDefaults(context.Context) (automation.EditorDefaults, error)
	Schedule(context.Context, string) (task.ScheduledWork, error)
	ConfigureSchedule(context.Context, string, automation.OwnerConfiguration) (automation.OwnerReceipt, error)
}
type scheduleEditor struct {
	Ref, Key, Instruction, Frequency, Date, Time, Timezone, Cron, Threshold, TaskRef string
	Version                                                                          int64
	Weekday, Day                                                                     string
	Enabled                                                                          bool
	BrowserTimezone, OriginalSpec, OriginalTimezone                                  string
}

func scheduleURL(ref string) string { return "/tasks/schedules/" + url.PathEscape(ref) }

func localScheduleTime(language string, v *task.Schedule) string {
	if v.Presentation != nil {
		if language == "zh-CN" {
			return v.Presentation.TimeChinese
		}
		return v.Presentation.TimeEnglish
	}
	return legacyLocalScheduleTime(language, v.Spec, v.Timezone)
}
func scheduleFrequency(language string, v *task.Schedule) string {
	if v.Presentation != nil {
		if language == "zh-CN" {
			return v.Presentation.FrequencyChinese
		}
		return v.Presentation.FrequencyEnglish
	}
	return translate(language, legacyScheduleFrequency(v.Type, v.Spec))
}

func (s *Server) loadSchedule(ctx context.Context, r *http.Request, p *page) error {
	if s.Schedules == nil {
		return errPageNotFound
	}
	e := &scheduleEditor{Key: sourceID()}
	p.Schedule = e
	if r.URL.Path == "/tasks/schedules/new" {
		defaults, err := s.Schedules.ScheduleDefaults(ctx)
		if err != nil {
			return err
		}
		applyScheduleDefaults(e, defaults)
		if loc := browserLocation(r); loc != nil {
			e.BrowserTimezone = loc.String()
		}
		return nil
	}
	ref := strings.TrimPrefix(r.URL.Path, "/tasks/schedules/")
	v, err := s.Schedules.Schedule(ctx, ref)
	if err != nil {
		return err
	}
	if v.Restricted {
		return task.ErrNotFound
	}
	var defaults automation.EditorDefaults
	if v.Presentation != nil && v.Presentation.EditorDefaults != nil {
		defaults = *v.Presentation.EditorDefaults
	} else {
		defaults = automation.LegacyEditorDefaults()
	}
	applyScheduleDefaults(e, defaults)
	e.Ref, e.Version, e.Instruction, e.Timezone, e.Enabled, e.TaskRef = v.Ref, v.Version, v.Instruction, v.Timezone, v.Enabled, v.TaskRef
	if v.Below != nil {
		e.Threshold = strconv.FormatInt(*v.Below, 10)
	}
	e.Frequency = "custom"
	e.Cron = v.Spec
	if v.Type == "at" {
		e.OriginalSpec, e.OriginalTimezone = v.Spec, v.Timezone
		e.Frequency = "once"
		if v.OwnerLocal != nil {
			e.Date, e.Time = v.OwnerLocal.Date, v.OwnerLocal.Clock
			return nil
		}
		return loadLegacyScheduleEditor(e, v)
	} else if v.OwnerFrequency != nil {
		e.Frequency, e.Time = v.OwnerFrequency.Frequency, v.OwnerFrequency.Clock
		if e.Frequency == "weekly" {
			e.Weekday = v.OwnerFrequency.Weekday
		}
		if e.Frequency == "monthly" {
			e.Day = v.OwnerFrequency.Day
		}
	} else {
		return loadLegacyScheduleEditor(e, v)
	}
	return nil
}

func decodeScheduleForm(r *http.Request) (*scheduleEditor, automation.OwnerConfiguration, error) {
	e := &scheduleEditor{}
	q := automation.OwnerConfiguration{}
	allowed := map[string]bool{"ref": true, "version": true, "source_id": true, "instruction": true, "frequency": true, "date": true, "time": true, "timezone": true, "cron": true, "threshold": true, "weekday": true, "day": true, "enabled": true, "original_spec": true, "original_timezone": true}
	get := r.PostForm.Get
	e.Ref, e.Key, e.Instruction, e.Frequency, e.Date, e.Time, e.Timezone, e.Cron, e.Threshold, e.Weekday, e.Day = get("ref"), get("source_id"), get("instruction"), get("frequency"), get("date"), get("time"), get("timezone"), get("cron"), get("threshold"), get("weekday"), get("day")
	e.Enabled = get("enabled") == "yes"
	e.OriginalSpec, e.OriginalTimezone = get("original_spec"), get("original_timezone")
	var err error
	e.Version, err = strconv.ParseInt(get("version"), 10, 64)
	// Retain the submitted editor before enforcing byte limits: maxlength counts
	// characters, and ordinary CJK input may exceed the domain's UTF-8 byte limit.
	for key, values := range r.PostForm {
		if !allowed[key] || len(values) != 1 || len(values[0]) > 8192 {
			return e, q, automation.ErrInvalid
		}
	}
	if err != nil || len(e.Key) != 32 || strings.Trim(e.Key, "0123456789abcdef") != "" || (get("enabled") != "" && get("enabled") != "yes") {
		return e, q, automation.ErrInvalid
	}
	q = automation.OwnerConfiguration{Ref: e.Ref, Version: e.Version, Instruction: e.Instruction, Timezone: e.Timezone, Enabled: e.Enabled}
	if e.Threshold != "" {
		value, err := strconv.ParseInt(e.Threshold, 10, 64)
		if err != nil {
			return e, q, automation.ErrInvalid
		}
		q.Below = &value
	}
	switch e.Frequency {
	case "once":
		q.Type = "at"
		q.LocalDate, q.LocalClock = e.Date, e.Time
		if e.Ref != "" && e.OriginalSpec != "" && e.OriginalTimezone == e.Timezone {
			q.PreserveSpec = e.OriginalSpec
		}

	case "custom":
		q.Type = "cron"
		q.Spec = e.Cron
	case "daily", "weekdays", "weekly", "monthly":
		q.Type = "cron"
		q.Frequency = &automation.OwnerFrequency{Frequency: e.Frequency, Clock: e.Time}
		if e.Frequency == "weekly" {
			q.Frequency.Weekday = e.Weekday
		}
		if e.Frequency == "monthly" {
			q.Frequency.Day = e.Day
		}

	default:
		err = automation.ErrInvalid
	}
	if err == nil {
		err = q.ValidateShape()
	}
	return e, q, err
}
func (s *Server) configureSchedule(w http.ResponseWriter, r *http.Request) error {
	if s.Schedules == nil {
		return errPageNotFound
	}
	e, q, err := decodeScheduleForm(r)
	if err == nil {
		var receipt automation.OwnerReceipt
		receipt, err = s.Schedules.ConfigureSchedule(r.Context(), e.Key, q)
		if err == nil {
			http.Redirect(w, r, scheduleURL(receipt.Ref)+"?notice=schedule-saved", http.StatusSeeOther)
			return nil
		}
	}
	message := "Check the instructions, schedule, time zone and threshold. One-time local times must be valid and unambiguous; enabled occurrences must be in the future."
	if errors.Is(err, agent.ErrConflict) {
		message = "This schedule changed or can no longer be edited. Your draft is preserved. Reload the saved schedule before making a new change."
	}
	if err != nil && !errors.Is(err, automation.ErrInvalid) && !errors.Is(err, agent.ErrConflict) {
		message = "The schedule could not be saved. Your draft is preserved; try again with the same form."
	}
	p := page{Tab: "/tasks", View: "schedule", Title: "Schedule settings", Path: r.URL.Path, Schedule: e, Error: message}
	if err := s.loadWorkbench(r.Context(), r, &p); err != nil {
		fail(w, http.StatusServiceUnavailable)
		return nil
	}
	if status, statusErr := s.Core.WebStatus(r.Context()); statusErr == nil {
		p.Status = status
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusConflict)
	s.render(w, r, p)
	return nil
}

// The owner confirms a saved revision. The editor draft never becomes executable input.
func (s *Server) trialSchedule(w http.ResponseWriter, r *http.Request) error {
	service, ok := s.Schedules.(interface {
		TrialSchedule(context.Context, string, string, int64) (string, error)
	})
	if !ok {
		return errPageNotFound
	}
	f := r.PostForm
	if len(f) != 3 || len(f["version"]) != 1 || len(f["source_id"]) != 1 || len(f["ref"]) != 1 {
		fail(w, 400)
		return nil
	}
	version, err := strconv.ParseInt(f.Get("version"), 10, 64)
	if err != nil {
		fail(w, 400)
		return nil
	}
	ref, err := service.TrialSchedule(r.Context(), f.Get("source_id"), f.Get("ref"), version)
	if err != nil {
		return err
	}
	http.Redirect(w, r, taskURL(ref), http.StatusSeeOther)
	return nil
}

func applyScheduleDefaults(e *scheduleEditor, v automation.EditorDefaults) {
	e.Frequency, e.Date, e.Time, e.Timezone, e.Cron, e.Weekday, e.Day, e.Enabled = v.Frequency, v.Date, v.Clock, v.Timezone, v.Cron, v.Weekday, v.Day, v.Enabled
}
