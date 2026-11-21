package memory

// InspectionPresentation is a Host-owned, request-local display projection.
// It is separate from both the stored inspection page and the private RPC schema.
type InspectionPresentation struct {
	CorrectionDraft        bool
	Program, ProgramDigest string
	Copy                   []InspectionTranslation
	CopyDigest             string
	ReaderRevision         string
	ViewSource             string
	ViewDigest             string
	Stylesheet             string
	StylesheetDigest       string
	Links                  []InspectionLink
	Source                 InspectionSourceLabels
	Origin                 InspectionOriginLabels
	Layer                  InspectionLayerLabels
	DefaultStatus          string
}
type InspectionLink struct{ Key, Value, URL string }
type InspectionSourceLabels struct{ Conversation, Task, Tool, Control, Other string }
type InspectionOriginLabels struct{ Written, Dream, Original, Automatic, Explicit, Other string }
type InspectionLayerLabels struct{ Transcripts, Representations, Injected, Notes string }

type InspectionStylesheet struct{ ReaderRevision, Source string }

type InspectionTranslation struct {
	English string `json:"english"`
	Chinese string `json:"chinese"`
}

type InspectionProgram struct{ ReaderRevision, Source string }
