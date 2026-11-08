package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/z-chenhao/assistant/internal/tool"
)

const ManageName = "builtin__memory_manage"
const layeredManageSchemaV3 = `{"type":"object","properties":{"operation":{"type":"string","enum":["remember","forget","list","read","write","retire","dream"]},"content":{"type":"string"},"memory_ref":{"type":"string"},"category":{"type":"string","enum":["health","finance","identity","relationships","goals","learning","preferences","facts","all"]},"supersedes":{"type":"string"},"layer":{"type":"string","enum":["notes","representations","injected"]},"title":{"type":"string"},"revision":{"type":"integer"},"links":{"type":"array","items":{"type":"string"}},"limit":{"type":"integer"},"offset":{"type":"integer"},"cursor":{"type":"string"}},"required":["operation"],"additionalProperties":false}`
const layeredManageSchemaV4 = `{"type":"object","properties":{"operation":{"type":"string","enum":["remember","forget","list","read","write","retire","dream"]},"content":{"type":"string"},"memory_ref":{"type":"string"},"category":{"type":"string","enum":["health","finance","identity","relationships","goals","learning","preferences","facts","all"]},"supersedes":{"type":"string"},"layer":{"type":"string","enum":["transcripts","notes","representations","injected"]},"title":{"type":"string"},"revision":{"type":"integer"},"links":{"type":"array","items":{"type":"string"}},"limit":{"type":"integer","minimum":0,"maximum":4096},"offset":{"type":"integer","minimum":0,"maximum":1073741824},"cursor":{"type":"string"}},"required":["operation"],"additionalProperties":false}`
const layeredManageSchemaV5 = `{"type":"object","properties":{"operation":{"type":"string","enum":["remember","forget","list","read","write","retire","dream"]},"content":{"type":"string"},"memory_ref":{"type":"string","description":"Complete opaque source_ref from a search hit. Copy it verbatim, including any compound components; do not extract an embedded UUID."},"category":{"type":"string","enum":["health","finance","identity","relationships","goals","learning","preferences","facts","all"]},"supersedes":{"type":"string"},"layer":{"type":"string","enum":["transcripts","notes","representations","injected"]},"title":{"type":"string"},"revision":{"type":"integer"},"links":{"type":"array","items":{"type":"string"}},"limit":{"type":"integer","minimum":0,"maximum":4096},"offset":{"type":"integer","minimum":0,"maximum":1073741824},"cursor":{"type":"string"}},"required":["operation"],"additionalProperties":false}`

// The operation branches share fields but expose their actual required inputs.
var ManageSchema = func() string {
	var schema map[string]any
	_ = json.Unmarshal([]byte(layeredManageSchemaV5), &schema)
	schema["oneOf"] = []any{
		map[string]any{"type": "object", "properties": map[string]any{"operation": map[string]any{"const": "write"}, "layer": map[string]any{"enum": []string{Notes, Representations, Injected}}, "title": map[string]any{"type": "string", "minLength": 1}, "content": map[string]any{"type": "string", "minLength": 1}}, "required": []string{"layer", "title", "content"}},
		map[string]any{"type": "object", "properties": map[string]any{"operation": map[string]any{"const": "retire"}, "layer": map[string]any{"enum": []string{Notes, Representations, Injected}}, "revision": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"layer", "memory_ref", "revision"}},
		map[string]any{"type": "object", "properties": map[string]any{"operation": map[string]any{"enum": []string{"remember", "forget", "list", "read", "dream"}}}},
	}
	body, _ := json.Marshal(schema)
	return string(body)
}()

// MutationError describes required fields, never the caller's private values.
func MutationError(raw string) string {
	var m Mutation
	if json.Unmarshal([]byte(raw), &m) != nil {
		return "Invalid memory arguments."
	}
	missing := []string{}
	if m.Operation == "" {
		missing = append(missing, "operation")
	}
	if m.Operation == "write" || m.Operation == "retire" {
		if m.Layer == "" {
			missing = append(missing, "layer")
		}
		if m.Operation == "write" {
			if strings.TrimSpace(m.Title) == "" {
				missing = append(missing, "title")
			}
			if strings.TrimSpace(m.Content) == "" {
				missing = append(missing, "content")
			}
		}
		if m.Operation == "retire" || m.MemoryRef != "" {
			if m.MemoryRef == "" {
				missing = append(missing, "memory_ref")
			}
			if m.Revision < 1 {
				missing = append(missing, "revision")
			}
		}
	}
	if len(missing) > 0 {
		return "Missing required memory fields: " + strings.Join(missing, ", ") + "."
	}
	return "Invalid memory arguments. Check the operation's field constraints and revision."
}

type Config struct {
	EncryptionKeyRef string `yaml:"encryption_key_ref"`
}

func (c Config) Valid() bool {
	return c.EncryptionKeyRef != "" && len(c.EncryptionKeyRef) <= 256 && !strings.ContainsAny(c.EncryptionKeyRef, "\n\r\x00")
}

type Mutation struct {
	Operation  string   `json:"operation"`
	Content    string   `json:"content,omitempty"`
	MemoryRef  string   `json:"memory_ref,omitempty"`
	Supersedes string   `json:"supersedes,omitempty"`
	Category   string   `json:"category,omitempty"`
	Layer      string   `json:"layer,omitempty"`
	Title      string   `json:"title,omitempty"`
	Revision   int64    `json:"revision,omitempty"`
	Links      []string `json:"links,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	Offset     int64    `json:"offset,omitempty"`
	Cursor     string   `json:"cursor,omitempty"`
}

func Definition() tool.Definition {
	d := tool.Definition{Host: true, Spec: tool.Spec{Name: ManageName, Version: 4, Description: "Read original evidence or derived memory documents. For source_type=conversation hits: operation=read, layer=transcripts, memory_ref=source_ref. For source_type=memory hits (notes/wikis/injected): operation=read, memory_ref=source_ref, omit layer. Read limit is 4..4096 bytes (0/default uses 4096); continue with next_offset. List limit is 1..50 (0/default uses 20); transcripts cannot be listed. Writes edit documents with memory_ref+revision. Main-only: remember/forget/dream", InputSchema: layeredManageSchemaV5, Permission: "memory_control", ReplayPolicy: "safe", Risk: "low", TimeoutMS: 10000, OutputBytes: 64 << 10, Provider: "builtin", ConflictPolicy: "none"}}
	legacy := d.Spec
	legacy.Version = 1
	legacy.OutputBytes = 4096
	legacy.InputSchema = legacyManageSchema
	legacy.Description = "Accept a user's explicit remember/correction or forget request in this user's Agent scope. Forget one memory_ref or opt out a category; remember with category resumes future extraction. Returns durable acceptance, not completed maintenance. Forget revokes unfinished context. Workers cannot write memory. Never submit secrets."
	layered := d.Spec
	layered.Version = 2
	layered.InputSchema = layeredManageSchemaV2
	layered.Description = layeredManageDescriptionV2
	v3 := d.Spec
	v3.Version = 3
	v3.Description = "Memory documents; edit with memory_ref+revision. Main-only: remember/forget/dream"
	v3.InputSchema = layeredManageSchemaV3
	v4 := d.Spec
	v4.InputSchema = layeredManageSchemaV4
	d.Spec.Version = 5
	d.Spec.Description += " References are opaque: copy the complete source_ref verbatim, never an embedded UUID."
	v5 := d.Spec
	d.Spec.Version = 6
	d.Spec.InputSchema = ManageSchema
	d.Spec.Description = "Memory documents. Copy complete source_ref verbatim. Read memory: refs without layer; conversation:/run: originals with layer=transcripts. Read limit 4..4096 bytes (0=4096); use next_offset. List limit 1..50 (0=20); transcripts cannot be listed. Write requires layer, title, content; edit also memory_ref+revision from read. Retire requires layer, memory_ref, revision. Main-only: remember/forget/dream."
	d.PreviousSpecs = []tool.Spec{legacy, layered, v3, v4, v5}
	return d
}

func Parse(raw string) (Mutation, bool) {
	var m Mutation
	if !tool.Validate(ManageSchema, raw) || json.Unmarshal([]byte(raw), &m) != nil || HasSecret(m.Content) {
		return m, false
	}
	if m.Limit < 0 || m.Limit > 4096 || m.Offset < 0 || m.Revision < 0 || len(m.MemoryRef) > 256 || len(m.Supersedes) > 256 || len(m.Cursor) > 1024 || m.Offset > 1<<30 {
		return m, false
	}
	if m.Operation == "read" && m.Layer == Transcripts {
		if !strings.HasPrefix(m.MemoryRef, "run:") {
			m.MemoryRef = tool.ObjectRef("conversation", m.MemoryRef)
		}
	} else {
		m.MemoryRef = tool.ObjectRef("memory", m.MemoryRef)
	}
	m.Supersedes = tool.ObjectRef("memory", m.Supersedes)
	validRef := func(v string) bool {
		return strings.HasPrefix(v, "memory:") && len(v) > 7 && !strings.ContainsAny(v, " /\\\n\r")
	}
	if m.Operation == "write" || m.Operation == "retire" {
		for i := range m.Links {
			m.Links[i] = tool.ObjectRef("memory", m.Links[i])
		}
		return m, m.Category == "" && m.Supersedes == "" && m.Limit == 0 && m.Offset == 0 && m.Cursor == "" && m.DocumentWrite().Valid()
	}
	if m.Operation == "dream" {
		return m, m.MemoryRef == "" && m.Layer == "" && m.Limit == 0 && m.Offset == 0 && m.Cursor == "" && m.Category == "" && m.Supersedes == "" && m.Title == "" && m.Revision == 0 && len(m.Links) == 0 && len(m.Content) <= 4096
	}
	if m.Operation == "list" || m.Operation == "read" {
		return m, m.Content == "" && m.Title == "" && m.Category == "" && m.Supersedes == "" && m.Revision == 0 && len(m.Links) == 0 && (m.Operation == "list" && m.Layer != Transcripts && m.MemoryRef == "" && m.Offset == 0 && m.Limit <= 50 || m.Operation == "read" && (validRef(m.MemoryRef) && m.Layer == "" || m.Layer == Transcripts && (strings.HasPrefix(m.MemoryRef, "conversation:") && len(m.MemoryRef) > 13 || strings.HasPrefix(m.MemoryRef, "run:") && len(m.MemoryRef) > 4) && !strings.ContainsAny(m.MemoryRef, " /\\\n\r")) && m.Cursor == "" && (m.Limit == 0 || m.Limit >= 4))
	}

	if m.Layer != "" || m.Title != "" || m.Revision != 0 || len(m.Links) != 0 || m.Limit != 0 || m.Offset != 0 || m.Cursor != "" || len(m.Content) > 4096 {
		return m, false
	}
	if m.Operation == "forget" {
		return m, (validRef(m.MemoryRef) && m.Category == "" || m.MemoryRef == "" && m.Category != "") && m.Content == "" && m.Supersedes == ""
	}
	if m.Category != "" {
		return m, m.Content == "" && m.MemoryRef == "" && m.Supersedes == ""
	}
	return m, strings.TrimSpace(m.Content) != "" && m.MemoryRef == "" && (m.Supersedes == "" || validRef(m.Supersedes))
}

// Categories are conservative opt-out filters, never diagnoses or new facts.
func Category(text string) string {
	s := strings.ToLower(text)
	for _, group := range []struct {
		name  string
		terms []string
	}{
		{"health", []string{"health", "sleep", "tired", "symptom", "diagnos", "medication", "睡", "病", "困", "药", "症状"}},
		{"finance", []string{"income", "salary", "asset", "employer options", "finance", "收支", "收入", "资产", "期权", "开销"}},
		{"identity", []string{"address", "passport", "birthday", "my age", "住址", "身份证", "护照", "生日", "我叫"}},
		{"relationships", []string{"family", "partner", "spouse", "家庭", "家人", "伴侣"}},
		{"goals", []string{"goal", "career", "independent development", "目标", "职业", "独立开发", "人生"}},
		{"learning", []string{"learning", "practic", "english", "学习", "练习", "英语"}},
		{"preferences", []string{"prefer", "like", "喜欢", "偏好"}},
	} {
		for _, term := range group.terms {
			if strings.Contains(s, term) {
				return group.name
			}
		}
	}
	return "facts"
}

var secretPattern = regexp.MustCompile(`(?i)(-----BEGIN .*PRIVATE KEY|\b(?:sk-[a-z0-9_-]{12,}|gh[pousr]_[a-z0-9]{12,}|eyJ[a-z0-9_-]{15,}\.)|(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|密码|私钥|支付密钥)\s*[:=：]\s*\S+)`)

func HasSecret(s string) bool {
	lower := strings.ToLower(s)
	for _, term := range []string{"password", "api key", "api_key", "access token", "access_token", "refresh token", "refresh_token", "private key", "凭据", "密钥", "密码", "令牌"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	return secretPattern.MatchString(s)
}
func Sensitive(s string) bool {
	s = strings.ToLower(s)
	for _, term := range []string{"health", "sleep", "symptom", "diagnos", "medication", "income", "salary", "asset", "option", "finance", "family", "address", "passport", "睡", "病", "症状", "药", "诊断", "收入", "资产", "期权", "家庭", "住址", "身份证", "护照"} {
		if strings.Contains(s, term) {
			return true
		}
	}
	return false
}
func Fingerprint(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Extract is deliberately conservative: retain short attributed reports, never
// turn a question, Tool trace or a diagnosis inferred by a Worker into a fact.
// It is deterministic so retry/restart cannot change the candidate set.
func Extract(text, author string) []string {
	if HasSecret(text) || len(text) > 16<<10 {
		return nil
	}
	var out []string
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '。' || r == '!' || r == '！' }) {
		sentence = strings.TrimSpace(sentence)
		lower := strings.ToLower(sentence)
		if len(sentence) < 4 || len(sentence) > 1024 || strings.ContainsAny(sentence, "?？") {
			continue
		}
		attributed := false
		if author == "worker" && strings.HasPrefix(sentence, "User reported: ") {
			sentence = strings.TrimSpace(strings.TrimPrefix(sentence, "User reported: "))
			lower = strings.ToLower(sentence)
			attributed = true
		}
		personal := strings.Contains(sentence, "我") || strings.HasPrefix(lower, "i ") || strings.HasPrefix(lower, "my ")
		if (author != "user" && !attributed) || !personal {
			continue
		}
		if strings.Contains(lower, "forget") || strings.Contains(sentence, "忘记") || strings.Contains(sentence, "不要记") {
			continue
		}
		out = append(out, sentence)
		if len(out) == 8 {
			break
		}
	}
	return out
}

func Stable(s string) bool {
	s = strings.ToLower(s)
	for _, term := range []string{"maybe", "might", "perhaps", "explor", "today", "recent", "tired", "sleepy", "也许", "可能", "探索", "只是想", "今天", "近期", "好困", "昨晚", "you said", "you suggested", "apparently", "你说", "他说", "她说", "听说", "好像"} {
		if strings.Contains(s, term) {
			return false
		}
	}
	return true
}

func (m Mutation) DocumentWrite() DocumentWrite {
	return DocumentWrite{Ref: m.MemoryRef, Layer: m.Layer, Title: m.Title, Content: m.Content, Revision: m.Revision, Links: m.Links, Retire: m.Operation == "retire"}
}

const legacyManageSchema = `{"type":"object","properties":{"operation":{"type":"string","enum":["remember","forget"]},"content":{"type":"string","minLength":1,"maxLength":4096},"memory_ref":{"type":"string","minLength":1,"maxLength":256},"category":{"type":"string","enum":["health","finance","identity","relationships","goals","learning","preferences","facts","all"]},"supersedes":{"type":"string","minLength":1,"maxLength":256}},"required":["operation"],"additionalProperties":false}`

// Exact deployed v2 snapshot, retained for durable replay.
const layeredManageSchemaV2 = `{"type":"object","properties":{"operation":{"type":"string","enum":["remember","forget","list","read","write","retire","dream"]},"content":{"type":"string","maxLength":49152},"memory_ref":{"type":"string"},"category":{"type":"string","enum":["health","finance","identity","relationships","goals","learning","preferences","facts","all"]},"supersedes":{"type":"string"},"layer":{"type":"string","enum":["notes","representations","injected"]},"title":{"type":"string"},"revision":{"type":"integer","minimum":0},"links":{"type":"array","items":{"type":"string"}},"limit":{"type":"integer","minimum":1,"maximum":4096},"offset":{"type":"integer","minimum":0},"cursor":{"type":"string"}},"required":["operation"],"additionalProperties":false}`
const layeredManageDescriptionV2 = "Scoped memory: list/read by layer; write creates Markdown, edits/retire require memory_ref and revision. dream queues consolidation. remember/forget are Main-only user controls. Workers may only list/read/write/retire. No credentials"
