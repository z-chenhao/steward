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

// Existing editor interpretation, owned by the same calendar version.
func editorProjection(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var facts contract.ProjectionFacts
	if err := rsiplugin.Decode(call.Input, &facts); err != nil {
		return rsiplugin.Result{}, err
	}
	bad := contract.ProjectionResult{ErrorCode: "INVALID_ARGUMENT"}
	if len(call.Inputs) != 0 || utf8.RuneCountInString(facts.Spec) > 256 || utf8.RuneCountInString(facts.Timezone) > 128 {
		return rsiplugin.JSONResult(ctx, bad)
	}
	if facts.Type == "at" {
		at, err := time.Parse(time.RFC3339Nano, facts.Spec)
		loc, zoneErr := calendarLocation(facts.Timezone)
		if err != nil || zoneErr != nil {
			return rsiplugin.JSONResult(ctx, bad)
		}
		return rsiplugin.JSONResult(ctx, contract.ProjectionResult{Frequency: "once", Date: at.In(loc).Format("2006-01-02"), Clock: at.In(loc).Format("15:04")})
	}
	if facts.Type != "cron" {
		return rsiplugin.JSONResult(ctx, bad)
	}
	value := contract.ProjectionResult{Frequency: "custom", Clock: "08:00"}
	fields := strings.Fields(facts.Spec)
	if len(fields) == 5 {
		minute, em := strconv.Atoi(fields[0])
		hour, eh := strconv.Atoi(fields[1])
		if em == nil && eh == nil && minute >= 0 && minute < 60 && hour >= 0 && hour < 24 {
			value.Clock = time.Date(2000, 1, 1, hour, minute, 0, 0, time.UTC).Format("15:04")
			switch {
			case fields[2] == "*" && fields[3] == "*" && fields[4] == "*":
				value.Frequency = "daily"
			case fields[2] == "*" && fields[3] == "*" && fields[4] == "1-5":
				value.Frequency = "weekdays"
			case fields[2] == "*" && fields[3] == "*" && len(fields[4]) == 1 && fields[4] >= "0" && fields[4] <= "6":
				value.Frequency = "weekly"
				value.Weekday = fields[4]
			case fields[3] == "*" && fields[4] == "*":
				if day, err := strconv.Atoi(fields[2]); err == nil && day >= 1 && day <= 31 {
					value.Frequency = "monthly"
					value.Day = fields[2]
				}
			}
		}
	}
	return rsiplugin.JSONResult(ctx, value)
}
