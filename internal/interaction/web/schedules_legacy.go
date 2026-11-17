package web

import (
	"github.com/z-chenhao/assistant/internal/task"
	"strconv"
	"strings"
	"time"
)

// Only NULL editor facts from graphs without schedule-editor/1 use this decoder.
// The authoritative reader rejects missing facts from graphs declaring it.
// New managed owner/CLI/Main writes freeze the owning plugin's projection.
// Retire this decoder when no such historical configuration remains.
func loadLegacyScheduleEditor(e *scheduleEditor, v task.ScheduledWork) error {
	if v.Type == "at" {
		at, err := time.Parse(time.RFC3339Nano, v.Spec)
		if err != nil {
			return err
		}
		loc, err := time.LoadLocation(v.Timezone)
		if err != nil {
			return err
		}
		e.Date, e.Time = at.In(loc).Format("2006-01-02"), at.In(loc).Format("15:04")
	} else {
		fields := strings.Fields(v.Spec)
		if len(fields) == 5 {
			minute, em := strconv.Atoi(fields[0])
			hour, eh := strconv.Atoi(fields[1])
			if em == nil && eh == nil && minute >= 0 && minute < 60 && hour >= 0 && hour < 24 {
				e.Time = time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC).Format("15:04")
				switch {
				case fields[2] == "*" && fields[3] == "*" && fields[4] == "*":
					e.Frequency = "daily"
				case fields[2] == "*" && fields[3] == "*" && fields[4] == "1-5":
					e.Frequency = "weekdays"
				case fields[2] == "*" && fields[3] == "*" && len(fields[4]) == 1 && fields[4] >= "0" && fields[4] <= "6":
					e.Frequency = "weekly"
					e.Weekday = fields[4]
				case fields[3] == "*" && fields[4] == "*":
					if day, err := strconv.Atoi(fields[2]); err == nil && day >= 1 && day <= 31 {
						e.Frequency = "monthly"
						e.Day = fields[2]
					}
				}
			}
		}
	}
	return nil
}

func legacyLocalScheduleTime(language, raw, zone string) string {
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return raw
	}
	format := "Jan 2, 2006 · 15:04"
	if language == "zh-CN" {
		format = "2006年1月2日 · 15:04"
	}
	return at.In(loc).Format(format) + " " + zone
}
func legacyScheduleFrequency(kind, spec string) string {
	if kind == "at" {
		return "Scheduled once"
	}
	f := strings.Fields(spec)
	if len(f) != 5 {
		return "Custom cron"
	}
	m, me := strconv.Atoi(f[0])
	h, he := strconv.Atoi(f[1])
	if me != nil || he != nil || m < 0 || m > 59 || h < 0 || h > 23 {
		return "Custom cron"
	}
	switch {
	case f[2] == "*" && f[3] == "*" && f[4] == "*":
		return "Every day"
	case f[2] == "*" && f[3] == "*" && f[4] == "1-5":
		return "Weekdays"
	case f[2] == "*" && f[3] == "*" && len(f[4]) == 1 && f[4] >= "0" && f[4] <= "6":
		return "Weekly"
	case f[3] == "*" && f[4] == "*":
		if day, err := strconv.Atoi(f[2]); err == nil && day >= 1 && day <= 31 {
			return "Monthly"
		}
	}
	return "Custom cron"
}
