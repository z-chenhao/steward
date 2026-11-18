package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type webPublicationReceipt struct {
	Revision string
	Name     string
}
type webResearchPage struct {
	Request   webaccess.PageRequest
	URL       string
	Page      webaccess.Page
	Image     []byte
	ImageMIME string
	ErrorCode string
}

// Only business composition executes in the plugin. Each resource resolves an
// original position, and all facts/publication remain with the original Tool.
func (t *tools) executeWebResearch(ctx context.Context, inv tool.Invocation, run agent.Run, name, args string) (tool.Result, error) {
	var input webaccess.ResearchInput
	if json.Unmarshal([]byte(args), &input) != nil {
		return tool.Failure("INVALID_ARGUMENT"), nil
	}
	if t.runs.plugins == nil {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	h := t.runs.pluginHostWithFence(t.fence)
	revision, err := h.Store.Binding(ctx, t.claim.RunID())
	if err != nil {
		return tool.Result{}, err
	}
	if revision == "" {
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	check := func(c context.Context) error {
		return t.runs.evidenceReadTx(c, func(tx *sql.Tx) error {
			_, n, a, e := t.acceptedBuiltin(c, tx, inv)
			if e != nil {
				return e
			}
			if n != name || a != args {
				return tool.ErrApprovalDenied
			}
			if e = t.runs.pluginDispatchFence(c, tx, t.claim.RunID()); e != nil {
				return e
			}
			if t.runs.webResourceIdentity(run, name) != runWebResource(run, name) {
				return rsi.ErrRevoked
			}
			return nil
		})
	}
	queries := input.Queries
	if len(queries) == 0 {
		if name == webaccess.CheckName {
			queries = []string{input.Claim}
		} else {
			queries = []string{input.Query}
		}
	}
	urls := input.URLs
	if input.URL != "" {
		urls = []string{input.URL}
	}
	queryFacts := map[int]webaccess.QueryReply{}
	pageFacts := map[int]webResearchPage{}
	modelFacts := map[int]webaccess.AnswerReply{}
	terminal := ""
	charged := false
	failure := func() (tool.Result, error) {
		if authority := t.runs.evidenceReadTx(context.WithoutCancel(ctx), func(tx *sql.Tx) error { return t.fence(context.WithoutCancel(ctx), tx) }); authority != nil {
			return tool.Result{}, authority
		}
		if charged {
			return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
		}
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	ctx, err = t.webHTMLContext(ctx, h, revision, check)
	if err != nil {
		return tool.Result{}, err
	}
	ctx, err = t.webImageContext(ctx, h, revision, check)
	if err != nil {
		return tool.Result{}, err
	}
	ctx, err = t.webExtractContext(ctx, h, revision, check)
	if err != nil {
		return tool.Result{}, err
	}
	ctx, err = t.webRouteContext(ctx, h, revision, check)
	if err != nil {
		return tool.Result{}, err
	}
	queryPort := rsi.Resource{Contract: webaccess.QueryContract, InputSchema: webQueryRequestSchema(), OutputSchema: webQueryReplySchema(), Invoke: func(c context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
		var q webaccess.QueryRequest
		if rsiplugin.DecodeResource(raw, &q) != nil || key != fmt.Sprintf("query:%d", q.Index) || q.Index < 0 || q.Index >= len(queries) || len(queries) > 8 || name != webaccess.SearchName && name != webaccess.CheckName || terminal != "" {
			return nil, tool.ErrApprovalDenied
		}
		if _, exists := queryFacts[q.Index]; exists {
			return nil, rsi.ErrConflict
		}
		actual := input.SearchQuery
		actual.Query = strings.TrimSpace(queries[q.Index])
		result, e := t.managedWebSearch(c, inv, run, q.Index, actual)
		if len(result.Attempts) > 0 || errors.Is(e, rsi.ErrUnknown) && !errors.Is(e, errWebSearchNoSourceDispatch) {
			charged = true
		}
		if errors.Is(e, webaccess.ErrSearchMethod) {
			return nil, e
		}
		reply := webaccess.QueryReply{Result: result, ErrorCode: webaccess.SourceErrorCode(e)}
		if reply.ErrorCode == "AUTH_REQUIRED" || strings.HasPrefix(reply.ErrorCode, "BUDGET_") || c.Err() != nil {
			terminal = reply.ErrorCode
		}
		payload, e := json.Marshal(reply)
		if e != nil {
			return nil, e
		}
		var original webaccess.QueryReply
		if json.Unmarshal(payload, &original) != nil {
			return nil, rsi.ErrInvalid
		}
		queryFacts[q.Index] = original
		return payload, nil
	}}
	pagePort := rsi.Resource{Contract: webaccess.PageContract, InputSchema: webPageRequestSchema(), OutputSchema: webPageReplySchema(), Invoke: func(c context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
		var q webaccess.PageRequest
		if rsiplugin.DecodeResource(raw, &q) != nil || key != fmt.Sprintf("page:%d", q.Slot) || q.Slot < 0 || q.Slot >= 8 || terminal != "" {
			return nil, tool.ErrApprovalDenied
		}
		if _, exists := pageFacts[q.Slot]; exists {
			return nil, rsi.ErrConflict
		}
		var url string
		if name == webaccess.ContentName {
			if q.Query != -1 || q.Source < 0 || q.Source >= len(urls) || len(urls) > 8 || q.Slot != q.Source {
				return nil, tool.ErrApprovalDenied
			}
			url = urls[q.Source]
		} else {
			if name != webaccess.SearchName && name != webaccess.CheckName || name == webaccess.SearchName && !input.IncludeContent || q.Slot >= 5 {
				return nil, tool.ErrApprovalDenied
			}
			original, ok := queryFacts[q.Query]
			if !ok || q.Source < 0 || q.Source >= len(original.Result.Sources) || len(queryFacts) != len(queries) {
				return nil, tool.ErrApprovalDenied
			}
			url = original.Result.Sources[q.Source].URL
			for _, old := range pageFacts {
				if old.URL == url {
					return nil, tool.ErrApprovalDenied
				}
			}
		}
		if err := check(c); err != nil {
			return nil, err
		}
		fetch := t.runs.webContentFetch
		if fetch == nil {
			return nil, rsi.ErrNotFound
		}
		page, e := fetch(c, t.runs.webAccess.Extraction(), url, input.Mode)
		if errors.Is(e, webcontent.ErrRouteUnavailable) || errors.Is(e, errWebHTMLMethod) || errors.Is(e, errWebImageMethod) || errors.Is(e, errWebExtractMethod) {
			return nil, e
		}
		observed := webResearchPage{Request: q, URL: url, Page: page, ErrorCode: webPageErrorCode(e)}
		if c.Err() != nil {
			terminal = "CANCELLED"
			observed.ErrorCode = terminal
		}
		if e == nil && page.Image != nil {
			if page.Image.MIMEType != "image/jpeg" && page.Image.MIMEType != "image/png" {
				return nil, rsi.ErrInvalid
			}
			image, decodeErr := base64.StdEncoding.DecodeString(page.Image.Data)
			if decodeErr != nil {
				return nil, rsi.ErrInvalid
			}
			observed.Image = image
			observed.ImageMIME = page.Image.MIMEType
		}
		observed.Page.Image = nil // image bytes stay with their original Host attachment
		if e != nil {
			observed.Page = webaccess.Page{URL: url}
		}
		payload, e := json.Marshal(webaccess.PageReply{Page: observed.Page, ErrorCode: observed.ErrorCode})
		if e != nil {
			return nil, e
		}
		var released webaccess.PageReply
		if json.Unmarshal(payload, &released) != nil {
			return nil, rsi.ErrInvalid
		}
		observed.Page = released.Page
		pageFacts[q.Slot] = observed
		return payload, nil
	}}
	answerPort := rsi.Resource{Contract: webaccess.AnswerContract, InputSchema: webAnswerRequestSchema(), OutputSchema: webAnswerReplySchema(), Invoke: func(c context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
		var q webaccess.AnswerRequest
		if rsiplugin.DecodeResource(raw, &q) != nil || terminal != "" || q.System == "" || q.User == "" {
			return nil, tool.ErrApprovalDenied
		}
		if _, exists := modelFacts[q.Index]; exists {
			return nil, rsi.ErrConflict
		}
		var modelKey, kind string
		if name == webaccess.ContentName {
			page, ok := pageFacts[q.Index]
			if !ok || page.ErrorCode != "" || input.Mode != "answer" || input.Prompt == "" || strings.HasPrefix(page.Page.MediaType, "image/") {
				return nil, tool.ErrApprovalDenied
			}
			modelKey, kind = fmt.Sprintf("answer:%d", q.Index), "page_answer"
		} else if name == webaccess.SearchName && input.Workflow != "" && input.Workflow != "none" && q.Index == 0 && len(queryFacts) == len(queries) {
			modelKey, kind = "summary", "summary"
		} else {
			return nil, tool.ErrApprovalDenied
		}
		if key != modelKey {
			return nil, tool.ErrApprovalDenied
		}
		if err := check(c); err != nil {
			return nil, err
		}
		previousCharged := charged
		charged = true
		result, e := t.webModelGeneration(c, inv, run, modelKey, webaccess.ModelCall{Kind: kind, Messages: []llm.Message{{Role: "system", Text: q.System}, {Role: "user", Text: q.User}}})
		// Existing generation receipts, including pending/failed admission, own
		// spending and uncertainty. There is no candidate-selected model/key.
		var status, errorCode, originalResult string
		if err := t.runs.evidenceReadTx(c, func(tx *sql.Tx) error {
			err := tx.QueryRowContext(c, `SELECT status,coalesce(error_code,''),coalesce(result_json,'') FROM model_generation WHERE run_id=? AND json_extract(request_json,'$.tool_generation.invocation_id')=? AND json_extract(request_json,'$.tool_generation.call_key')=?`, run.ID, inv.ID, modelKey).Scan(&status, &errorCode, &originalResult)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}); err != nil {
			return nil, err
		}
		charged = previousCharged || status == "pending" || status == "completed" || status == "failed" && errorCode != "BUDGET_APPROVAL_REQUIRED" && errorCode != "BUDGET_DENIED"
		if status == "pending" {
			return nil, rsi.ErrUnknown
		}
		if e == nil {
			var accepted llm.GenerationResult
			if status != "completed" || json.Unmarshal([]byte(originalResult), &accepted) != nil || accepted.Text != result.Text {
				return nil, rsi.ErrInvalid
			}
			result = accepted
		}
		reply := webaccess.AnswerReply{Text: result.Text, ErrorCode: webaccess.SourceErrorCode(e)}
		if status == "failed" && (errorCode == "BUDGET_APPROVAL_REQUIRED" || errorCode == "BUDGET_DENIED") {
			reply.ErrorCode = errorCode
		}
		if reply.ErrorCode != "" {
			terminal = reply.ErrorCode
		}
		modelFacts[q.Index] = reply
		return json.Marshal(reply)
	}}
	rawInput, _ := json.Marshal(webaccess.ResearchRequest{Operation: name, Input: input, ContextWindow: run.Snapshot.ContextWindow})
	raw, err := h.DriveService(ctx, revision, webaccess.ResearchContract, rawInput, nil, check, []rsi.Resource{queryPort, pagePort, answerPort})
	t.runs.host.boundary("after_web_research_method")
	if authority := t.runs.evidenceReadTx(context.WithoutCancel(ctx), func(tx *sql.Tx) error { return t.fence(context.WithoutCancel(ctx), tx) }); authority != nil {
		return tool.Result{}, authority
	}
	// A trusted terminal receipt is authoritative even if the candidate ignores
	// it, fails or returns a different plan. No further resource dispatch is
	// allowed; the existing public error normalizer remains authoritative.
	if terminal != "" {
		return tool.Failure(terminal), nil
	}
	if err != nil {
		return failure()
	}
	var plan webaccess.ResearchPlan
	if rsiplugin.DecodeResult(raw, &plan) != nil {
		return failure()
	}
	if plan.ErrorCode != "" {
		invalidAnswerMedia := false
		for _, page := range pageFacts {
			invalidAnswerMedia = invalidAnswerMedia || name == webaccess.ContentName && input.Mode == "answer" && strings.HasPrefix(page.Page.MediaType, "image/")
		}
		if plan.ErrorCode == "INVALID_ARGUMENT" && (invalidAnswerMedia || len(queries) > 8 || name == webaccess.ContentName && (len(urls) == 0 || len(urls) > 8 || input.Mode == "answer" && input.Prompt == "")) {
			return tool.Failure(plan.ErrorCode), nil
		}
		return failure()
	}
	if (name == webaccess.SearchName || name == webaccess.CheckName) && len(queryFacts) != len(queries) {
		return failure()
	}
	if name == webaccess.ContentName && len(pageFacts) != len(urls) {
		return failure()
	}
	result, err := materializeWebResearch(name, input, plan, queryFacts, pageFacts, modelFacts)
	if err != nil {
		return failure()
	}
	result.HostFence = webPublicationReceipt{Revision: revision, Name: name}
	return result, nil
}

func webPageErrorCode(err error) string {
	if err == nil {
		return ""
	}
	for _, code := range []string{"WEB_FETCH_FAILED", "WEB_HTTP_OR_ACCESS_ERROR", "WEB_HTTP_ERROR", "WEB_LOGIN_OR_ACCESS_REQUIRED", "WEB_ADDRESS_BLOCKED", "WEB_REDIRECT_LIMIT", "WEB_BODY_LIMIT", "WEB_UNSUPPORTED_ENCODING", "WEB_EMPTY_OR_DYNAMIC_PAGE", "WEB_EXTRACTOR_UNAVAILABLE", "WEB_EXTRACTION_FAILED", "INVALID_IMAGE", "WEB_UNSUPPORTED_CONTENT", "INVALID_ARGUMENT", "PROVIDER_UNAVAILABLE", "RESOURCE_LIMIT", "CANCELLED"} {
		if err.Error() == code {
			return code
		}
	}
	return "WEB_FETCH_FAILED"
}

func materializeWebResearch(name string, input webaccess.ResearchInput, plan webaccess.ResearchPlan, queries map[int]webaccess.QueryReply, pages map[int]webResearchPage, models map[int]webaccess.AnswerReply) (tool.Result, error) {
	bad := func() (tool.Result, error) { return tool.Result{}, rsi.ErrInvalid }
	document := webaccess.Document{Version: 1}
	errors := []webaccess.ResearchError{}
	for index := 0; index < len(queries); index++ {
		q, ok := queries[index]
		if !ok {
			return bad()
		}
		document.Queries = append(document.Queries, q.Result)
		if q.ErrorCode != "" {
			errors = append(errors, webaccess.ResearchError{Code: q.ErrorCode})
		}
	}
	seen := map[int]bool{}
	attachments := []tool.AttachmentOutput{}
	for _, index := range plan.Pages {
		page, ok := pages[index]
		if !ok || page.ErrorCode != "" || seen[index] {
			return bad()
		}
		seen[index] = true
		document.Pages = append(document.Pages, page.Page)
		if page.Image != nil {
			extension := "jpg"
			if page.ImageMIME == "image/png" {
				extension = "png"
			} else if page.ImageMIME != "image/jpeg" {
				return bad()
			}
			attachments = append(attachments, tool.AttachmentOutput{BinaryOutput: tool.BinaryOutput{Name: fmt.Sprintf("web-image-%d.%s", index, extension), MediaType: page.ImageMIME, Reader: bytes.NewReader(page.Image)}, Image: true})
		}
	}
	for _, page := range pages {
		if page.ErrorCode == "" && !slices.Contains(plan.Pages, page.Request.Slot) {
			return bad()
		}
	}
	// Error ordering follows original resource positions, not map iteration.
	for index := 0; index < len(pages); index++ {
		page, ok := pages[index]
		if !ok {
			return bad()
		}
		if page.ErrorCode != "" {
			errors = append(errors, webaccess.ResearchError{URL: page.URL, Code: page.ErrorCode})
		}
	}
	if name == webaccess.CheckName {
		if plan.Status != "unclear" && plan.Status != "missing-evidence" {
			return bad()
		}
		document.Claim, document.Status = input.Claim, plan.Status
		for _, choice := range plan.Passages {
			if choice.Page < 0 || choice.Page >= len(document.Pages) {
				return bad()
			}
			page := document.Pages[choice.Page]
			text, ok := webOriginalRange(page.Text, choice.Start, choice.End)
			if !ok {
				return bad()
			}
			document.Passages = append(document.Passages, webaccess.Passage{URL: page.URL, Text: text, Start: choice.Start, End: choice.End, ContentHash: webaccess.Hash(text), SourceHash: webaccess.Hash(page.Text)})
		}
	} else if plan.Status != "" || len(plan.Passages) > 0 {
		return bad()
	}
	if len(plan.SummaryJoin) > 32 || strings.TrimSpace(plan.SummaryJoin) != "" {
		return bad()
	}
	parts := []string{}
	used := map[int]bool{}
	for _, choice := range plan.Summary {
		model, ok := models[choice.Index]
		if !ok || model.ErrorCode != "" || used[choice.Index] {
			return bad()
		}
		text, ok := webOriginalRange(model.Text, 0, choice.Bytes)
		if !ok {
			return bad()
		}
		used[choice.Index] = true
		parts = append(parts, text)
	}
	if len(used) != len(models) {
		return bad()
	}
	summary := strings.Join(parts, plan.SummaryJoin)
	if plan.SummaryTrailing && len(parts) > 0 {
		summary += plan.SummaryJoin
	}
	if name == webaccess.ContentName && input.Mode == "answer" && len(models) != len(document.Pages) || name == webaccess.SearchName && input.Workflow != "" && input.Workflow != "none" && len(models) != 1 {
		return bad()
	}
	if len(plan.Preview.QueryBytes) != len(document.Queries) || len(plan.Preview.PageBytes) != len(document.Pages) {
		return bad()
	}
	preview := document
	preview.Queries = append([]webaccess.SearchResult{}, document.Queries...)
	preview.Pages = append([]webaccess.Page{}, document.Pages...)
	for i := range preview.Queries {
		value, ok := webOriginalRange(preview.Queries[i].Text, 0, plan.Preview.QueryBytes[i])
		if !ok {
			return bad()
		}
		preview.Queries[i].Text = value
	}
	for i := range preview.Pages {
		value, ok := webOriginalRange(preview.Pages[i].Text, 0, plan.Preview.PageBytes[i])
		if !ok {
			return bad()
		}
		preview.Pages[i].Text = value
	}
	previewSummary, ok := webOriginalRange(summary, 0, plan.Preview.SummaryBytes)
	if !ok {
		return bad()
	}
	if plan.Preview.OmitDocument {
		preview = webaccess.Document{Version: 1, Status: document.Status}
	}
	body, err := json.Marshal(document)
	if err != nil || len(body) > webaccess.MaxEvidenceBytes {
		return tool.Failure("RESOURCE_LIMIT"), nil
	}
	envelope := webaccess.ResearchEnvelope{Document: preview, Summary: previewSummary, ReviewRequired: name == webaccess.SearchName && input.Workflow == "summary-review", Errors: errors, HasMore: true, Untrusted: true, FilterGuarantee: "source URLs filtered locally; provider dates/answers are not a strict guarantee", Cost: "external service fees are outside the model-token budget"}
	// The process is finished before Artifact commit; this closure only stamps
	// the real reference and serializes already proved, immutable source values.
	return tool.Result{Attachments: attachments, Output: &tool.BinaryOutput{Name: "web-access-evidence.json", MediaType: "application/json", Reader: bytes.NewReader(body), ResultJSON: func(ref string) string {
		envelope.ResponseID = ref
		out, _ := json.Marshal(envelope)
		return string(out)
	}}}, nil
}
func webOriginalRange(text string, start, end int) (string, bool) {
	if start < 0 || end < start || end > len(text) {
		return "", false
	}
	if start < len(text) && !utf8.RuneStart(text[start]) || end < len(text) && !utf8.RuneStart(text[end]) {
		return "", false
	}
	return text[start:end], true
}

func (t *tools) commitWebPublication(ctx context.Context, tx *sql.Tx, id string, receipt webPublicationReceipt) error {
	if err := t.fence(ctx, tx); err != nil {
		return err
	}
	if receipt.Name != webaccess.SearchName && receipt.Name != webaccess.ContentName && receipt.Name != webaccess.CheckName && receipt.Name != webaccess.FetchName {
		return tool.ErrApprovalDenied
	}
	var expected string
	var managed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run b,json_each(b.managed_services_json) s WHERE b.run_id=? AND s.value='steward.web-extract/1')`, t.claim.RunID()).Scan(&managed); err != nil {
		return err
	}
	if managed {
		expected = t.runs.webAccess.ScopeIdentity()
	} else {
		expected = t.runs.webAccess.Identity()
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tool_invocation i JOIN rsi_run b USING(run_id) JOIN run r USING(run_id) JOIN capability_snapshot c ON c.snapshot_id=r.capability_snapshot_id,json_each(c.tools_json) s WHERE i.invocation_id=? AND i.run_id=? AND i.status='running' AND i.tool_name=? AND b.graph_revision=? AND json_extract(s.value,'$.name')=i.tool_name AND json_extract(s.value,'$.resource_hash')=?)`, id, t.claim.RunID(), receipt.Name, receipt.Revision, expected).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return rsi.ErrRevoked
	}
	return t.runs.pluginDispatchFence(ctx, tx, t.claim.RunID())
}
