package intelligentui

import (
	"encoding/json"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
)

func TestMeetingViewRetainsOriginalChoicesWithoutInventedDates(t *testing.T) {
	c := Component{Name: "scheduleTime", Props: json.RawMessage(`{"title":"Unused alternate title","reasonForScheduling":"Review 中文 <b>raw</b>","meetingDuration":30.5,"timeSlots":[{"date":"2026-11-17","time":"09:15","duration":"ignored original hint"},{"date":"2026-11-18","time":"14:00"}]}`)}
	got, ok := meetingView(3, c)
	want := contract.MeetingView{Component: 3, Title: "Review 中文 <b>raw</b>", Slots: []contract.MeetingSlot{{Choice: 0, Date: "2026-11-17", Time: "09:15", Duration: "30.5 min"}, {Choice: 1, Date: "2026-11-18", Time: "14:00", Duration: "30.5 min"}}, DeclineLabel: "None of these work", SelectedTitle: "Time Selected", DeclinedTitle: "No Time Selected", SavingLabel: "Saving selection…", SelectError: "Your selection was not confirmed. Retry the same time.", DeclineError: "Your selection was not confirmed. Retry the same choice.", SavedLabel: "Time preference saved."}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("owning view changed original choices or text: %#v", got)
	}
	for _, bad := range []Component{{Name: "scheduleTime", Props: json.RawMessage(`{}`)}, {Name: "steward.form", Props: c.Props}, {Name: "scheduleTime", Props: json.RawMessage(`{"reasonForScheduling":"R","meetingDuration":0,"timeSlots":[{"date":"Tomorrow","time":"10"}]}`)}} {
		if _, ok := meetingView(0, bad); ok {
			t.Fatal("invented default meeting for invalid/incomplete input")
		}
	}
}

func TestMeetingViewMatchesIndependentReceiptCorpus(t *testing.T) {
	raw, err := os.ReadFile("../../rsi/builtin/ui-meeting-original.json")
	if err != nil {
		t.Fatal(err)
	}
	var samples []struct {
		Contract string          `json:"contract"`
		Document string          `json:"document"`
		Expected json.RawMessage `json:"expected"`
	}
	if err = json.Unmarshal(raw, &samples); err != nil || len(samples) != 2 {
		t.Fatal("lost independent receipt corpus", err)
	}
	for _, sample := range samples {
		var actual any
		if sample.Contract == contract.Normalize {
			actual = Normalize(sample.Document)
		} else {
			view, ok := ParsePreview(sample.Document)
			data, _ := json.Marshal(view)
			actual = contract.PreviewResult{Valid: ok, PreviewJSON: string(data)}
		}
		wire, _ := json.Marshal(actual)
		got, _ := rsiplugin.Canonical(json.RawMessage(wire))
		want, _ := rsiplugin.Canonical(sample.Expected)
		if string(got) != string(want) {
			t.Fatalf("independent %s receipt mismatch\ngot %s\nwant %s", sample.Contract, got, want)
		}
	}
}
