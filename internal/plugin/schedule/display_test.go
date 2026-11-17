package schedule

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func TestScheduleDisplayPreservesCurrentBilingualCopy(t *testing.T) {
	for _, c := range []struct{ kind, spec, zone, en, zh, timeEn, timeZh string }{
		{"at", "2030-01-02T08:30:45.123456789+08:00", "Asia/Shanghai", "Scheduled once", "单次计划", "Jan 2, 2030 · 08:30 Asia/Shanghai", "2030年1月2日 · 08:30 Asia/Shanghai"},
		{"cron", "30 8 * * *", "UTC", "Every day", "每天", "", ""},
		{"cron", "30 8 * * 1-5", "UTC", "Weekdays", "工作日", "", ""},
		{"cron", "30 8 * * 0", "UTC", "Weekly", "每周", "", ""},
		{"cron", "30 8 31 * *", "UTC", "Monthly", "每月", "", ""},
		{"cron", "*/15 9-17 * * 1-5", "UTC", "Custom cron", "自定义 Cron", "", ""},
	} {
		input, _ := json.Marshal(contract.DisplayFacts{Type: c.kind, Spec: c.spec, Timezone: c.zone})
		out, err := Plugin().Call(context.Background(), rsiplugin.Call{Contract: contract.Display, Input: input})
		var value contract.DisplayResult
		expected := contract.DisplayResult{FrequencyEnglish: c.en, FrequencyChinese: c.zh, TimeEnglish: c.timeEn, TimeChinese: c.timeZh}
		if err != nil || rsiplugin.Decode(out.Output, &value) != nil || value != expected || !tool.Validate(contract.DisplayOutputSchema, string(out.Output)) || out.State != nil || out.OutputData != nil {
			t.Fatal(c, value, err)
		}
	}
}
