package tool

// BuiltinFamily names a Host transaction/execution boundary. Model-visible
// names and frozen execution policy remain in Spec.
type BuiltinFamily string

const (
	UIBuiltin           BuiltinFamily = "ui"
	TaskBuiltin         BuiltinFamily = "task"
	ArtifactBuiltin     BuiltinFamily = "artifact"
	ImageBuiltin        BuiltinFamily = "image"
	AutomationBuiltin   BuiltinFamily = "automation"
	ComputerBuiltin     BuiltinFamily = "computer"
	CodeBuiltin         BuiltinFamily = "code"
	EnvironmentBuiltin  BuiltinFamily = "environment"
	AgentBuiltin        BuiltinFamily = "agent"
	MemoryWriteBuiltin  BuiltinFamily = "memory_write"
	MemorySearchBuiltin BuiltinFamily = "memory_search"
	EvidenceBuiltin     BuiltinFamily = "evidence"
	SkillBuiltin        BuiltinFamily = "skill"
	SearchBuiltin       BuiltinFamily = "tool_search"
	WebAccessBuiltin    BuiltinFamily = "web_access"
)

type builtinPolicy struct {
	family     BuiltinFamily
	permission string
	internal   bool
}

// This is the single trusted name/policy catalog used for validation and Host
// dispatch. A provider cannot register an arbitrary Host name or permission.
var builtinCatalog = map[string]builtinPolicy{
	"builtin__skill_list":          {SkillBuiltin, "read_only", false},
	"builtin__skill_view":          {SkillBuiltin, "read_only", false},
	"builtin__web_search":          {WebAccessBuiltin, "read_only", false},
	"builtin__web_fetch":           {WebAccessBuiltin, "read_only", false},
	"builtin__fetch_content":       {WebAccessBuiltin, "read_only", false},
	"builtin__get_search_content":  {WebAccessBuiltin, "read_only", false},
	"builtin__source_check":        {WebAccessBuiltin, "read_only", false},
	"builtin__intelligent_ui_show": {UIBuiltin, "ui_present", false},
	"builtin__tool_search":         {SearchBuiltin, "read_only", false},
	"builtin__read":                {EnvironmentBuiltin, "read_only", false},
	"builtin__bash":                {EnvironmentBuiltin, "external_effect", false},
	"builtin__edit":                {EnvironmentBuiltin, "external_effect", false},
	"builtin__write":               {EnvironmentBuiltin, "external_effect", false},
	"builtin__task_search":         {TaskBuiltin, "read_only", false},
	"builtin__task_manage":         {TaskBuiltin, "task_control", false},
	"builtin__artifact_search":     {ArtifactBuiltin, "read_only", false},
	"builtin__artifact_manage":     {ArtifactBuiltin, "artifact_write", false},
	"builtin__image_ocr":           {ImageBuiltin, "read_only", false},
	"builtin__automation_manage":   {AutomationBuiltin, "task_control", false},
	"builtin__automation_observe":  {AutomationBuiltin, "automation_observe", false},
	"builtin__computer_discover":   {ComputerBuiltin, "read_only", true},
	"builtin__computer_bind":       {ComputerBuiltin, "read_only", true},
	"builtin__computer_invoke":     {ComputerBuiltin, "read_only", true},
	"builtin__codemode_execute":    {CodeBuiltin, "read_only", false},
	"builtin__agent_run":           {AgentBuiltin, "spawn_agents", false},
	"builtin__memory_manage":       {MemoryWriteBuiltin, "memory_control", false},
	"builtin__memory_search":       {MemorySearchBuiltin, "read_only", false},
	"builtin__evidence_read":       {EvidenceBuiltin, "read_only", false},
}

func HostFamily(name string) (BuiltinFamily, bool) {
	p, ok := builtinCatalog[name]
	return p.family, ok
}
