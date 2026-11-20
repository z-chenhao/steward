package web

// These are the existing, concrete Host view entries and display fields. They
// are private/experimental; this does not publish page or Task domain models.
var workbenchViewNames = []string{"tasks-page", "tasks-list", "execution-page", "execution-header", "execution-side", "execution-progress", "execution-result", "tasks-navigation", "task-page", "task-header", "task-decisions", "task-progress", "task-messages", "task-milestones", "tasks-attention", "task-feedback", "conversation-page", "attention-summary", "conversation", "approvals", "schedule-editor-page"}
var workbenchSharedNames = []string{"icon-arrow-right", "icon-attach", "icon-attention", "icon-configuration", "icon-filter", "icon-progress", "icon-refresh", "icon-search", "icon-send"}

const workbenchViewFields = "Action Approval ApprovalNext Approvals Artifacts At Attention Before Below BrowserTimezone Bubbles CanEnable CanStart CanStop Children Choices ClientSource CompletionPolicy ComposerModels ConfigVersion Configurations Count CreatedAt Cron Current Date Day Decisions DeliveriesBefore Description Detail Draft DraftTask Enabled Epoch Event Execution Executions Failed Fallback FeedbackDraft Feedbacks FirstPending Frequency GitHubRule GoalSummary HTML History HistoryNext ID Instruction Instructions InvalidField IssueComments Issues Items Key Kind Label Language Links List Messages MinBelow Mode More MoreInstructions Name NeedsAttention NeedsMe Next NextAt OriginalSpec OriginalTimezone OtherTask Path PendingCount Picker PlaceholderLabel Previewable Progress ProgressSummary Projection Prompt Query Queued ReaderRevision Readiness Ready Reason RecentRuns Ref ReplyKey Restricted Role Running Schedule Scheduled ScheduledNext SchedulesUnavailable Scope Sequence SourceID SourceSequence Spec Stage Status Succeeded TaskRef TaskTitle Tasks TasksNext Text Threshold ThresholdRejected Time Timezone Title Type URL UTCOffset UTCOffsets Unavailable UpdatedAt Version View Waiting Weekday"

// Permissible committed display regions; the owner chooses subsets and order.
// It cannot add a browser binding to an unrelated/private DOM container.
var workbenchLiveSlots = map[string]map[string]bool{
	"conversation":   {"workspace-status": true, "conversation-attention": true, "conversation-messages": true, "conversation-approvals": true},
	"attention":      {"workspace-status": true, "tasks-attention": true},
	"tasks":          {"workspace-status": true, "ongoing-work": true},
	"configurations": {"workspace-status": true, "ongoing-work": true},
	"execution":      {"workspace-status": true, "execution-header": true, "execution-progress": true, "task-messages": true, "execution-side": true},
	"task":           {"workspace-status": true, "task-header": true, "task-feedback": true, "task-progress": true, "task-decisions": true, "task-messages": true, "task-milestones": true},
	"settings":       {"workspace-status": true, "signin-status": true},
}
