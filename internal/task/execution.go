package task

import "github.com/z-chenhao/assistant/internal/conversation"

// Execution is one dispatched Root, including historical epochs. It is a
// read-only owner view, separate from Task state and saved Automation intent.
type Execution struct {
	ID, TaskRef, Title, CompletionPolicy, Status, CreatedAt, UpdatedAt string
	Epoch                                                              int64
	NeedsAttention                                                     bool
}
type ExecutionList struct {
	Items []Execution
	Next  string
}
type ExecutionDetail struct {
	Execution
	Description     string
	ProgressSummary string
	RecentRuns      []Execution
	Instructions    []Instruction
	Messages        []conversation.Message
}
