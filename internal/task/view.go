package task

import (
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/automation"
	"github.com/z-chenhao/assistant/internal/conversation"
)

var ErrNotFound = errors.New("task not found")

// ListQuery is a user-facing read query, separate from model-visible search.
type ListQuery struct {
	View, Query, Cursor string
	Limit               int
	// NeedsMe filters executions independently of their status.
	NeedsMe bool
}

func (q ListQuery) Validate() error {
	if q.View != "" && q.View != "open" && q.View != "attention" && q.View != "closed" && q.View != "all" {
		return ErrInvalid
	}
	if len(q.Query) > 300 || len(q.Cursor) > 1024 || q.Limit < 0 || q.Limit > 50 {
		return ErrInvalid
	}
	return nil
}

func ValidRef(ref string) bool {
	return strings.HasPrefix(ref, "task:") && len(ref) > 5 && len(ref) <= 256 && !strings.ContainsAny(ref, "/\\\x00\r\n")
}

type Progress struct {
	Root, Wait                                  string
	Queued, Running, Succeeded, Failed, Waiting int
	Approval, Input, Unknown                    bool
}

func (p Progress) Children() int   { return p.Queued + p.Running + p.Succeeded + p.Failed + p.Waiting }
func (p Progress) Attention() bool { return p.Approval || p.Input || p.Unknown || p.Root == "failed" }
func (p Progress) Stage(status string) string {
	switch status {
	case "paused":
		return "Paused"
	case "cancelled":
		return "Cancelled"
	case "completed":
		return "Completed"
	}
	switch {
	case p.Unknown:
		return "External action outcome is unconfirmed"
	case p.Approval:
		return "Waiting for your approval"
	case p.Input:
		return "Waiting for your input"
	case p.Root == "failed":
		return "This attempt failed; the task remains open"
	case p.Root == "queued":
		return "Queued"
	case p.Root == "running":
		return "Working"
	case p.Root == "cancelled":
		return "This attempt stopped; the task remains open"
	case p.Root == "completed" && p.Queued+p.Running+p.Waiting == 0:
		return "This round is complete; the task remains open"
	case p.Root == "waiting" || p.Root == "completed":
		if p.Queued+p.Running+p.Waiting > 0 {
			return "Waiting for delegated work"
		}
		if p.Wait == "external_event" {
			return "Waiting for an external event"
		}
		return "Waiting for available resources"
	default:
		return "Ready for the next request"
	}
}

type Entry struct {
	Summary
	Stage, CreatedAt string
	CompletionPolicy string
	Schedule         *Schedule
	NeedsAttention   bool
	Epoch            int64
	Progress         Progress
}

// Schedule is owner-facing Automation configuration. It is not a promised
// execution time or a frozen Run snapshot.
type Schedule struct {
	Presentation                                   *automation.SchedulePresentation `json:"-"`
	Ref, Instruction, Type, Spec, Timezone, NextAt string
	CreatedAt, UpdatedAt                           string
	Version                                        int64
	Enabled, Restricted                            bool
	Below                                          *int64
}

// ScheduledWork may exist before a Task has been dispatched. TaskRef is present
// only when the scheduler has actually linked one; Ref remains Automation identity.
type ScheduledWork struct {
	OwnerLocal     *automation.OwnerLocal     `json:"-"`
	OwnerFrequency *automation.OwnerFrequency `json:"-"`
	TaskRef        string
	Schedule
}
type Instruction struct {
	Text, At string
	Epoch    int64
}
type List struct {
	Items []Entry
	Next  string
}
type History struct{ ID, Kind, Text, At string }
type Detail struct {
	Entry
	Artifacts        []string
	History          []History
	HistoryNext      string
	DeliveriesBefore int64
	Messages         []conversation.Message
	ApprovalIDs      []string
	SourceSequence   int64
	Instructions     []Instruction
	MoreInstructions bool
}
