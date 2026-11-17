package schedule

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func scheduleDisplay(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.DisplayFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	bad := contract.DisplayResult{ErrorCode: "INVALID_ARGUMENT"}
	if len(call.Inputs) != 0 || utf8.RuneCountInString(facts.Spec) > 256 || utf8.RuneCountInString(facts.Timezone) > 128 || (facts.Type != "at" && facts.Type != "cron") {
		return rsiplugin.JSONResult(ctx, bad)
	}
	label := displayFrequency(facts.Type, facts.Spec)
	chinese := map[string]string{"Scheduled once": "单次计划", "Every day": "每天", "Weekdays": "工作日", "Weekly": "每周", "Monthly": "每月", "Custom cron": "自定义 Cron"}[label]
	value := contract.DisplayResult{FrequencyEnglish: label, FrequencyChinese: chinese}
	if facts.Type == "at" {
		at, err := time.Parse(time.RFC3339Nano, facts.Spec)
		loc, zoneErr := calendarLocation(facts.Timezone)
		if err != nil || zoneErr != nil {
			return rsiplugin.JSONResult(ctx, bad)
		}
		value.TimeEnglish = at.In(loc).Format("Jan 2, 2006 · 15:04") + " " + facts.Timezone
		value.TimeChinese = at.In(loc).Format("2006年1月2日 · 15:04") + " " + facts.Timezone
	}
	return rsiplugin.JSONResult(ctx, value)
}

func displayFrequency(kind, spec string) string {
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
