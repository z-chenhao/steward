package demonstration

import (
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"testing"
)

func TestReviewedStepsMustTraverseAnUnambiguousSampleToItsGoal(t *testing.T) {
	open := contract.Step{Action: "activate", Label: "Preview"}
	topic := contract.Step{Action: "enter", Label: "Topic"}
	q := contract.DemoRequest{Name: "Preview report", Steps: []contract.Step{topic, open}, Goal: 2, Sample: []contract.Transition{{From: 0, Step: topic, To: 1}, {From: 1, Step: open, To: 2}}}
	good, err := Review(q)
	if err != nil || !good.Verified || good.Matched != 2 {
		t.Fatal(good, err)
	}
	q.Steps = []contract.Step{open, topic}
	wrong, err := Review(q)
	if err != nil || wrong.Verified || wrong.Matched != 0 {
		t.Fatal(wrong, err)
	}
	q.Sample = append(q.Sample, q.Sample[0])
	if _, err = Review(q); err == nil {
		t.Fatal("ambiguous sample accepted")
	}
}
