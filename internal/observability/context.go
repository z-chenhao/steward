package observability

// Context inspection projects existing frozen records, never a current Run
// checkpoint or model configuration. Text offsets count UTF-8 bytes.
const ContextChunk = 8192
const ContextPage = 40

type RoundQuery struct {
	RunID string
	After int64
}

func (q RoundQuery) Validate() error {
	if q.RunID == "" || len(q.RunID) > 128 || q.After < -1 {
		return ErrInvalid
	}
	return nil
}

type Round struct {
	ID        string `json:"generation_id"`
	Step      int64  `json:"step"`
	Status    string `json:"status"`
	Error     string `json:"error_code,omitempty"`
	CreatedAt string `json:"created_at"`
}

type Rounds struct {
	Items []Round `json:"items"`
	Next  *int64  `json:"next,omitempty"`
}

type ContextQuery struct {
	RunID               string
	Step                int64
	Section             string
	Item, Offset, After int
	JSON                bool
}

func (q ContextQuery) Validate() error {
	if (RoundQuery{RunID: q.RunID, After: q.Step}).Validate() != nil || q.Step < 0 || q.Item < 0 || q.Item > 1<<30 || q.Offset < 0 || q.Offset > 1<<30 || q.After < 0 || q.After > 1<<30 {
		return ErrInvalid
	}
	switch q.Section {
	case "messages", "tools", "response", "provenance", "recall":
	default:
		return ErrInvalid
	}
	return nil
}

type ContextItem struct {
	Index      int    `json:"index"`
	Label      string `json:"label"`
	SourceKind string `json:"source_kind,omitempty"`
	SourceRef  string `json:"source_ref,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type FrozenContext struct {
	Round       Round         `json:"round"`
	RequestHash string        `json:"request_hash"`
	Previous    *int64        `json:"previous_step,omitempty"`
	Items       []ContextItem `json:"items"`
	Next        *int          `json:"next,omitempty"`
	Selected    *ContextItem  `json:"selected,omitempty"`
	Content     string        `json:"content"`
	Length      int           `json:"length"`
	NextOffset  *int          `json:"next_offset,omitempty"`
}

type DiffQuery struct {
	ReaderRevision string
	RunID          string
	Step           int64
	Section        string
	After          int
}

func (q DiffQuery) Validate() error {
	if !ValidReaderRevision(q.ReaderRevision) || q.Section != "messages" && q.Section != "tools" {
		return ErrInvalid
	}
	return (ContextQuery{RunID: q.RunID, Step: q.Step, Section: q.Section, After: q.After}).Validate()
}

type ContextChange struct {
	Kind   string `json:"kind"`
	Before *int   `json:"before,omitempty"`
	After  *int   `json:"after,omitempty"`
	Label  string `json:"label"`
}

type ContextDiff struct {
	ReaderRevision string          `json:"reader_revision,omitempty"`
	Previous       *int64          `json:"previous_step,omitempty"`
	Items          []ContextChange `json:"items"`
	Next           *int            `json:"next,omitempty"`
}
