package web

import (
	"html/template"

	"github.com/z-chenhao/assistant/internal/task"
)

// This is a private display binding, not page, Task.Detail, Conversation or an
// approval domain object. Even printf on a complete record cannot reveal Raw,
// hashes, original message envelopes, credentials or undisplayed provenance.
type workbenchApprovalRef struct{ ID string }
type workbenchProjection struct {
	HTML           template.HTML
	Spec, Fallback string
}
type workbenchMessage struct {
	Role, ReplyKey, ClientSource, CreatedAt string
	Sequence                                int64
	Projection                              workbenchProjection
	Bubbles                                 []workbenchProjection
	Approval                                *workbenchApprovalRef
}
type workbenchProgress struct{ Children, Queued, Running, Succeeded, Failed, Waiting int }
type workbenchSchedule struct {
	Ref, Instruction, Type, Spec, Timezone, NextAt, CreatedAt, UpdatedAt string
	FrequencyLabel, TimeLabel                                            string
	Version                                                              int64
	Enabled, Restricted                                                  bool
	Below                                                                *int64
}
type workbenchInstruction struct {
	Text, At string
	Epoch    int64
}
type workbenchHistory struct{ ID, Kind, Text, At string }
type workbenchEntry struct {
	TaskRef, Title, GoalSummary, Status, Stage, CreatedAt, UpdatedAt, CompletionPolicy, ProgressSummary string
	NeedsAttention                                                                                      bool
	Epoch                                                                                               int64
	Progress                                                                                            workbenchProgress
	Schedule                                                                                            *workbenchSchedule
}
type workbenchDetail struct {
	workbenchEntry
	SourceSequence   int64
	Instructions     []workbenchInstruction
	MoreInstructions bool
	History          []workbenchHistory
	HistoryNext      string
	DeliveriesBefore int64
}
type workbenchExecution struct {
	ID, TaskRef, Title, CompletionPolicy, Status, CreatedAt, UpdatedAt string
	Epoch                                                              int64
	NeedsAttention                                                     bool
}
type workbenchExecutionDetail struct {
	workbenchExecution
	Description, ProgressSummary string
	RecentRuns                   []workbenchExecution
	Instructions                 []workbenchInstruction
}
type workbenchScheduled struct {
	workbenchSchedule
	TaskRef string
}
type workbenchTasks struct {
	GitHubRule     *githubRuleView
	Configurations bool
	Query          struct {
		View, Query, Cursor string
		NeedsMe             bool
	}
	List struct {
		Items []workbenchEntry
		Next  string
	}
	Detail     *workbenchDetail
	Executions struct {
		Items []workbenchExecution
		Next  string
	}
	Execution            *workbenchExecutionDetail
	Messages             []workbenchMessage
	Approvals            []workbenchApprovalRef
	Artifacts            []shownArtifact
	Scheduled            []workbenchScheduled
	ScheduledNext        string
	SchedulesUnavailable bool
}
type workbenchFeedback struct {
	Ref, TaskRef, TaskTitle, Kind, Prompt, Draft string
	SourceSequence                               int64
	Artifacts                                    []shownArtifact
}
type workbenchAttention struct {
	Feedbacks                     []workbenchFeedback
	Approvals                     []workbenchApprovalRef
	Issues                        []attentionIssue
	Count                         int
	More, Unavailable             bool
	Next, ApprovalNext, TasksNext string
}
type workbenchViewData struct {
	ReaderRevision                                 string
	Tasks                                          *workbenchTasks
	Attention                                      *workbenchAttention
	Schedule                                       *scheduleEditor
	ComposerModels                                 composerModels
	Status                                         Status
	Language, Path, FeedbackDraft, SourceID, Draft string
	DraftTask                                      *taskContext
	Messages                                       []workbenchMessage
	Approvals                                      []workbenchApprovalRef
	PendingCount                                   int
	FirstPending                                   string
	Before                                         int64
}

// Opaque Host fragments are validated/restored after the passive owner markup.
// The candidate can place them, but cannot inspect/rewrite their trusted bytes.
type workbenchBindings struct {
	opaque    map[string]template.HTML
	approvals map[string]approvalView
}

func (b *workbenchBindings) fragment(value template.HTML) template.HTML {
	if value == "" {
		return ""
	}
	key := sourceID()
	placeholder := template.HTML(`<span data-workbench-content="` + key + `"></span>`)
	b.opaque[string(placeholder)] = value
	return placeholder
}
func (b *workbenchBindings) approval(v approvalView) workbenchApprovalRef {
	b.approvals[v.ID] = v
	return workbenchApprovalRef{ID: v.ID}
}
func (b *workbenchBindings) messages(in []shownMessage) []workbenchMessage {
	out := make([]workbenchMessage, 0, len(in))
	for _, v := range in {
		m := workbenchMessage{Role: v.Role, Sequence: v.Sequence, ReplyKey: v.ReplyKey, ClientSource: v.ClientSource, CreatedAt: v.CreatedAt, Projection: workbenchProjection{HTML: b.fragment(v.Projection.HTML), Spec: v.Projection.Spec, Fallback: v.Projection.Fallback}}
		for _, bubble := range v.Bubbles {
			m.Bubbles = append(m.Bubbles, workbenchProjection{HTML: b.fragment(bubble.HTML), Spec: bubble.Spec, Fallback: bubble.Fallback})
		}
		if v.Approval != nil {
			ref := b.approval(*v.Approval)
			m.Approval = &ref
		}
		out = append(out, m)
	}
	return out
}
func workbenchInstructions(in []task.Instruction) []workbenchInstruction {
	out := make([]workbenchInstruction, 0, len(in))
	for _, v := range in {
		out = append(out, workbenchInstruction{Text: v.Text, At: v.At, Epoch: v.Epoch})
	}
	return out
}
func workbenchScheduleOf(v *task.Schedule, language string) *workbenchSchedule {
	if v == nil {
		return nil
	}
	return &workbenchSchedule{Ref: v.Ref, Instruction: v.Instruction, Type: v.Type, Spec: v.Spec, Timezone: v.Timezone, NextAt: v.NextAt, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Version: v.Version, Enabled: v.Enabled, Restricted: v.Restricted, Below: v.Below, FrequencyLabel: scheduleFrequency(language, v), TimeLabel: localScheduleTime(language, v)}
}
func workbenchEntryOf(v task.Entry, language string) workbenchEntry {
	return workbenchEntry{TaskRef: v.TaskRef, Title: v.Title, GoalSummary: v.GoalSummary, Status: v.Status, Stage: v.Stage, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, CompletionPolicy: v.CompletionPolicy, ProgressSummary: v.ProgressSummary, NeedsAttention: v.NeedsAttention, Epoch: v.Epoch, Schedule: workbenchScheduleOf(v.Schedule, language), Progress: workbenchProgress{Children: v.Progress.Children(), Queued: v.Progress.Queued, Running: v.Progress.Running, Succeeded: v.Progress.Succeeded, Failed: v.Progress.Failed, Waiting: v.Progress.Waiting}}
}
func workbenchExecutionOf(v task.Execution) workbenchExecution {
	return workbenchExecution{ID: v.ID, TaskRef: v.TaskRef, Title: v.Title, CompletionPolicy: v.CompletionPolicy, Status: v.Status, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Epoch: v.Epoch, NeedsAttention: v.NeedsAttention}
}
func workbenchData(p page, language string) (workbenchViewData, *workbenchBindings) {
	b := &workbenchBindings{opaque: map[string]template.HTML{}, approvals: map[string]approvalView{}}
	data := workbenchViewData{Schedule: p.Schedule, ComposerModels: p.ComposerModels, Status: p.Status, Language: language, Path: p.Path, FeedbackDraft: p.FeedbackDraft, SourceID: p.SourceID, Draft: p.Draft, DraftTask: p.DraftTask, PendingCount: p.PendingCount, FirstPending: p.FirstPending, Before: p.Before}
	if p.Workbench != nil {
		data.ReaderRevision = p.Workbench.ReaderRevision
	}
	data.Messages = b.messages(p.Messages)
	for _, a := range p.Approvals {
		data.Approvals = append(data.Approvals, b.approval(a))
	}
	if v := p.Tasks; v != nil {
		t := &workbenchTasks{GitHubRule: v.GitHubRule, Configurations: v.Configurations, Artifacts: v.Artifacts, ScheduledNext: v.ScheduledNext, SchedulesUnavailable: v.SchedulesUnavailable}
		t.Query.View, t.Query.Query, t.Query.Cursor, t.Query.NeedsMe = v.Query.View, v.Query.Query, v.Query.Cursor, v.Query.NeedsMe
		t.List.Next, t.Executions.Next = v.List.Next, v.Executions.Next
		for _, x := range v.List.Items {
			t.List.Items = append(t.List.Items, workbenchEntryOf(x, language))
		}
		for _, x := range v.Executions.Items {
			t.Executions.Items = append(t.Executions.Items, workbenchExecutionOf(x))
		}
		for _, x := range v.Scheduled {
			t.Scheduled = append(t.Scheduled, workbenchScheduled{workbenchSchedule: *workbenchScheduleOf(&x.Schedule, language), TaskRef: x.TaskRef})
		}
		if x := v.Detail; x != nil {
			d := &workbenchDetail{workbenchEntry: workbenchEntryOf(x.Entry, language), DeliveriesBefore: x.DeliveriesBefore, SourceSequence: x.SourceSequence, Instructions: workbenchInstructions(x.Instructions), MoreInstructions: x.MoreInstructions, HistoryNext: x.HistoryNext}
			for _, h := range x.History {
				d.History = append(d.History, workbenchHistory{ID: h.ID, Kind: h.Kind, Text: h.Text, At: h.At})
			}
			t.Detail = d
		}
		if x := v.Execution; x != nil {
			e := &workbenchExecutionDetail{workbenchExecution: workbenchExecutionOf(x.Execution), Description: x.Description, ProgressSummary: x.ProgressSummary, Instructions: workbenchInstructions(x.Instructions)}
			for _, r := range x.RecentRuns {
				e.RecentRuns = append(e.RecentRuns, workbenchExecutionOf(r))
			}
			t.Execution = e
		}
		t.Messages = b.messages(v.Messages)
		for _, a := range v.Approvals {
			t.Approvals = append(t.Approvals, b.approval(a))
		}
		data.Tasks = t
	}
	if v := p.Attention; v != nil {
		a := &workbenchAttention{Issues: v.Issues, Count: v.Count, More: v.More, Unavailable: v.Unavailable, Next: v.Next, ApprovalNext: v.ApprovalNext, TasksNext: v.TasksNext}
		for _, f := range v.Feedbacks {
			a.Feedbacks = append(a.Feedbacks, workbenchFeedback{Ref: f.Ref, TaskRef: f.TaskRef, TaskTitle: f.TaskTitle, Kind: f.Kind, Prompt: f.Prompt, Draft: f.Draft, SourceSequence: f.SourceSequence, Artifacts: f.Artifacts})
		}
		for _, x := range v.Approvals {
			a.Approvals = append(a.Approvals, b.approval(x))
		}
		data.Attention = a
	}
	return data, b
}
