package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/meetingprep/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func meetingPreparationCases() []rsi.Case {
	result := []rsi.Case{}
	for _, test := range []struct {
		subject  string
		position int
	}{{"Re: Launch review", 0}, {"Other meeting", -1}} {
		input, _ := json.Marshal(contract.Input{Meetings: []string{"Launch review"}, Subjects: []string{test.subject}})
		expected, _ := json.Marshal(contract.Pair{Meeting: test.position, Thread: test.position})
		result = append(result, rsi.Case{InstanceID: "meeting-preparation", Contract: contract.Select, Input: input, Expected: expected})
	}
	return result
}
