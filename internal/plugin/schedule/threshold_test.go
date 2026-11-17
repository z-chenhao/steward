package schedule

import (
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/schedule/contract"
	"github.com/z-chenhao/assistant/internal/tool"
)

func TestThresholdEntrySequenceAndLateObservations(t *testing.T) {
	entered := false
	values := []int64{2200, 2190, 1750, 1740, 2050, 1780}
	want := []bool{false, false, true, false, false, true}
	for i, value := range values {
		result, err := thresholdTransition(contract.ThresholdFacts{Value: value, Below: 1800, Entered: entered, Fresh: true})
		if err != nil || result.Notify != want[i] {
			t.Fatal(i, result, err)
		}
		entered = result.IsBelow
	}
	for _, prior := range []bool{false, true} {
		result, err := thresholdTransition(contract.ThresholdFacts{Value: 1, Below: 1800, Entered: prior, Fresh: false})
		if err != nil || result.Notify || result.IsBelow != prior {
			t.Fatal("late evidence changed accepted threshold state", result, err)
		}
	}
}

func TestThresholdBoundsAndPrivateFacts(t *testing.T) {
	for _, value := range []int64{0, 1800, 1000000000000} {
		result, err := thresholdTransition(contract.ThresholdFacts{Value: value, Below: value, Fresh: true})
		if err != nil || result.Notify || result.IsBelow {
			t.Fatal("equal boundary entered threshold", result, err)
		}
	}
	for _, facts := range []contract.ThresholdFacts{{Value: -1}, {Value: 1000000000001}, {Below: -1}, {Below: 1000000000001}} {
		if _, err := thresholdTransition(facts); err == nil {
			t.Fatal("out-of-contract observation accepted", facts)
		}
	}
	for _, raw := range []string{`{"value":1,"below":2,"entered":false,"fresh":true,"fire_id":"private"}`, `{"value":1,"below":2}`, `{"value":1,"below":2,"entered":false,"fresh":true,"notify":true}`} {
		if tool.Validate(contract.ThresholdInputSchema, raw) {
			t.Fatal("authority or incomplete facts accepted", raw)
		}
	}
}
