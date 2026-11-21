package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/meetingprep/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func meetingPreparationCases(calendarOnly bool) []rsi.Case {
	result := []rsi.Case{}
	for _, test := range []struct {
		subject  string
		position int
	}{{"Re: Launch review", 0}, {"Other meeting", -1}} {
		input, _ := json.Marshal(contract.Input{Meetings: []string{"Launch review"}, Subjects: []string{test.subject}})
		meeting := test.position
		if calendarOnly {
			meeting = 0
		}
		expected, _ := json.Marshal(contract.Pair{Meeting: meeting, Thread: test.position})
		result = append(result, rsi.Case{InstanceID: "meeting-preparation", Contract: contract.Select, Input: input, Expected: expected})
	}
	if calendarOnly {
		// Adding a mailbox must keep the same first meeting, even when only a
		// later event has related mail.
		input, _ := json.Marshal(contract.Input{Meetings: []string{"Planning", "Launch review"}, Subjects: []string{"Re: Launch review"}})
		expected, _ := json.Marshal(contract.Pair{Meeting: 0, Thread: -1})
		result = append(result, rsi.Case{InstanceID: "meeting-preparation", Contract: contract.Select, Input: input, Expected: expected})
	}
	return result
}
