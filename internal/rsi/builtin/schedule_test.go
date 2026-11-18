package builtin

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/schedule"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestScheduleOwnerCasesCoverInstalledPackageDiagnostics(t *testing.T) {
	plugin := schedule.Plugin()
	for _, c := range scheduleCases(plugin.Version) {
		got, err := plugin.Services[c.Contract](context.Background(), rsiplugin.Call{Contract: c.Contract, Input: c.Input})
		var actual, expected any
		if err != nil || json.Unmarshal(got.Output, &actual) != nil || json.Unmarshal(c.Expected, &expected) != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s(%s) returned %s; expected %s: %v", c.Contract, c.Input, got.Output, c.Expected, err)
		}
	}
}
