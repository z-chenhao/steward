package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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

var errWebSearchNoSourceDispatch = errors.New("WEB_SEARCH_NO_SOURCE_DISPATCH")

// Only the original accepted query can enter the source port. Query text,
// filters, endpoint/credentials and the model snapshot are not candidate fields.
func acceptedWebQueryAt(name, args string, index int, q webaccess.SearchQuery) bool {
	var original struct {
		webaccess.SearchQuery
		Queries []string `json:"queries"`
		Claim   string   `json:"claim"`
	}
	if (name != webaccess.SearchName && name != webaccess.CheckName) || json.Unmarshal([]byte(args), &original) != nil {
		return false
	}
	queries := original.Queries
	if len(queries) == 0 {
		if name == webaccess.CheckName {
			queries = []string{original.Claim}
		} else {
			queries = []string{original.Query}
		}
	}
	if index < 0 || index >= len(queries) {
		return false
	}
	return q.Query == strings.TrimSpace(queries[index]) && q.Provider == original.Provider && q.NumResults == original.NumResults && q.RecencyFilter == original.RecencyFilter && slices.Equal(q.DomainFilter, original.DomainFilter)
}

func (t *tools) managedWebSearch(ctx context.Context, inv tool.Invocation, run agent.Run, index int, q webaccess.SearchQuery) (result webaccess.SearchResult, failure error) {
	base := webaccess.SearchResultBase(q.Query)
	businessFailure, sourceAttempted := false, false
	defer func() {
		if failure != nil && !businessFailure {
			if sourceAttempted {
				failure = errors.Join(rsi.ErrUnknown, failure)
			} else {
				failure = errors.Join(errWebSearchNoSourceDispatch, failure)
			}
			failure = errors.Join(webaccess.ErrSearchMethod, failure)
		}
	}()
	if t.runs.plugins == nil {
		return base, rsi.ErrNotFound
	}
	check := func(c context.Context) error {
		return t.runs.evidenceReadTx(c, func(tx *sql.Tx) error {
			_, name, args, err := t.acceptedBuiltin(c, tx, inv)
			if err != nil {
				return err
			}
			if !acceptedWebQueryAt(name, args, index, q) {
				return tool.ErrApprovalDenied
			}
			if err = t.runs.pluginDispatchFence(c, tx, t.claim.RunID()); err != nil {
				return err
			}
			if t.runs.webResourceIdentity(run, name) != runWebResource(run, name) {
				return rsi.ErrRevoked
			}
			return nil
		})
	}
	if err := check(ctx); err != nil {
		return base, err
	}
	h := *t.runs.plugins
	store := t.runs.host.Plugins()
	store.Fence = t.fence
	h.Store = store
	revision, err := store.Binding(ctx, t.claim.RunID())
	if err != nil || revision == "" {
		if err == nil {
			err = rsi.ErrNotFound
		}
		return base, err
	}
	config := t.runs.webAccess
	requiresInstructions, err := t.runs.managedRunService(ctx, t.claim.RunID(), webaccess.ResearchContract)
	if err != nil {
		return base, err
	}
	formatted, err := t.runs.managedRunService(ctx, t.claim.RunID(), webaccess.NormalizeContract)
	if err != nil {
		return base, err
	}
	order := append([]string{}, config.Order()...)
	allowed := append([]string{}, order...)
	if q.Provider != "" && q.Provider != "auto" {
		allowed = nil
		for _, source := range order {
			if source == q.Provider {
				allowed = []string{source}
			}
		}
	}
	observations := map[string]webaccess.SourceReply{}
	chosenCounts := map[string]int{}
	var lastError error
	terminal := false
	attempts := []webaccess.Attempt{}
	port := rsi.Resource{Contract: webaccess.SourceContract, InputSchema: json.RawMessage(`{"type":"object","properties":{"provider":{"type":"string","enum":["native","exa","searxng","tavily"]},"num_results":{"type":"integer","minimum":1,"maximum":20},"instructions":{"type":"string","maxLength":8192}},"required":["provider","num_results"],"additionalProperties":false}`), OutputSchema: webSourceReplySchema(), Invoke: func(c context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
		var request webaccess.SourceRequest
		if rsiplugin.DecodeResource(raw, &request) != nil || key != request.Provider || len(attempts) >= len(allowed) || !slices.Contains(allowed, request.Provider) || (len(config.SourceOrder) > 0 && allowed[len(attempts)] != request.Provider) || slices.ContainsFunc(attempts, func(attempt webaccess.Attempt) bool { return attempt.Provider == request.Provider }) || terminal || q.Validate() != nil || (q.NumResults != 0 && q.NumResults != request.NumResults) {
			return nil, tool.ErrApprovalDenied
		}
		if request.Provider != "native" && request.Instructions != "" {
			return nil, tool.ErrApprovalDenied
		}
		// Actual pre-research snapshots retain their original hosted method.
		// New managed research cannot turn missing instructions into native policy.
		if request.Provider == "native" && requiresInstructions && request.Instructions == "" {
			return nil, tool.ErrApprovalDenied
		}
		// Exa's configured auto fallback is not a new primary-search grant.
		// An explicit per-query provider choice retains its original semantics.
		if request.Provider == "exa" && len(config.SourceOrder) == 0 && (q.Provider == "" || q.Provider == "auto") {
			if _, attempted := observations["native"]; !attempted {
				return nil, tool.ErrApprovalDenied
			}
		}
		if len(attempts) > 0 && !webaccess.SourceFallbackAllowed(lastError) {
			return nil, tool.ErrApprovalDenied
		}
		if err := check(c); err != nil {
			return nil, err
		}
		actual := q
		actual.NumResults = request.NumResults
		var hosted webaccess.QuerySearch
		if run.Snapshot.ProviderKind == "openai_account" {
			hosted = func(c context.Context, q webaccess.SearchQuery) (llm.GenerationResult, error) {
				var messages []llm.Message
				if request.Instructions != "" {
					messages = []llm.Message{{Role: "system", Text: request.Instructions}, {Role: "user", Text: q.Query}}
				}
				return t.webModelGeneration(c, inv, run, fmt.Sprintf("search:%d", index), webaccess.ModelCall{Kind: "hosted_search", Query: &q, Messages: messages})
			}
		}
		sourceAttempted = true
		observation, e := t.runs.readWebSource(c, request.Provider, actual, hosted)
		if e == nil {
			if formatted {
				hasWireFacts := observation.Format != ""
				observation, e = normalizeWebSource(c, h, revision, observation)
				if hasWireFacts {
					t.runs.host.boundary("after_web_source_format")
				}
				if e != nil && !errors.Is(e, errWebFormatLimit) {
					return nil, e
				}
			} else {
				observation, e = webaccess.LegacyFormatSourceV2(observation)
			}
		}
		lastError = e
		terminal = e == nil || !webaccess.SourceFallbackAllowed(e)
		outcome := "completed"
		if e != nil {
			outcome = webaccess.SourceAttemptCode(e)
		}
		attempts = append(attempts, webaccess.Attempt{Provider: request.Provider, Outcome: outcome})
		payload, encodeErr := json.Marshal(webaccess.SourceFacts(observation, e))
		if encodeErr != nil {
			return nil, encodeErr
		}
		// Keep the exact immutable scalar facts released on the wire, including
		// JSON's string normalization; never retain a provider's mutable slice.
		var released webaccess.SourceReply
		if json.Unmarshal(payload, &released) != nil {
			return nil, rsi.ErrInvalid
		}
		observations[request.Provider] = released
		chosenCounts[request.Provider] = request.NumResults
		return payload, nil
	}}
	input, _ := json.Marshal(webaccess.SearchRequest{Query: q, Sources: order, OrderExplicit: len(config.SourceOrder) > 0})
	raw, err := h.DriveService(ctx, revision, webaccess.SearchContract, input, nil, check, []rsi.Resource{port})
	if err != nil {
		return base, err
	}
	var plan webaccess.SearchPlan
	if rsiplugin.DecodeResult(raw, &plan) != nil {
		return base, rsi.ErrInvalid
	}
	base.Attempts = attempts
	if q.Validate() != nil {
		if len(attempts) == 0 && plan.Provider == "" && plan.ErrorCode == "INVALID_ARGUMENT" {
			businessFailure = true
			return base, errors.New("INVALID_ARGUMENT")
		}
		return base, rsi.ErrInvalid
	}
	if len(allowed) == 0 {
		if len(attempts) == 0 && plan.Provider == "" && plan.ErrorCode == "PERMISSION_DENIED" {
			businessFailure = true
			return base, errors.New("PERMISSION_DENIED")
		}
		return base, rsi.ErrInvalid
	}
	if plan.ErrorCode != "" {
		if len(attempts) == 0 || lastError == nil || plan.Provider != "" || len(plan.Sources) != 0 || webaccess.SourceErrorCode(lastError) != plan.ErrorCode {
			return base, rsi.ErrInvalid
		}
		businessFailure = true
		return base, lastError
	}
	if len(attempts) == 0 || lastError != nil || plan.Provider != attempts[len(attempts)-1].Provider || len(plan.Sources) > chosenCounts[plan.Provider] {
		return base, rsi.ErrInvalid
	}
	original, ok := observations[plan.Provider]
	if !ok {
		return base, rsi.ErrInvalid
	}
	seen := map[string]bool{}
	for _, choice := range plan.Sources {
		if choice.Index < 0 || choice.Index >= len(original.Sources) {
			return base, rsi.ErrInvalid
		}
		source := original.Sources[choice.Index]
		if choice.TitleBytes < 0 || choice.TitleBytes > len(source.Title) || !formatted && choice.TitleBytes > 1024 || !utf8.ValidString(source.Title[:choice.TitleBytes]) || seen[source.URL] || !webSourceAllowed(source.URL, q.DomainFilter) {
			return base, rsi.ErrInvalid
		}
		seen[source.URL] = true
		source.Title = source.Title[:choice.TitleBytes]
		base.Sources = append(base.Sources, llm.WebSource{URL: source.URL, Title: source.Title})
	}
	switch plan.TextMode {
	case "source":
		if len(q.DomainFilter) > 0 {
			return base, rsi.ErrInvalid
		}
		base.Text = original.Text
	case "sources":
		base.Text = "Source URLs filtered locally; consult originals for evidence.\n"
		for _, source := range base.Sources {
			base.Text += source.Title + "\n" + source.URL + "\n"
		}
	default:
		return base, rsi.ErrInvalid
	}
	base.Provider, base.RetrievedAt = plan.Provider, original.RetrievedAt
	if usage := original.Usage; usage != nil {
		base.Usage = &llm.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens}
	}
	return base, nil
}
func runWebResource(run agent.Run, name string) string {
	for _, spec := range run.Tools {
		if spec.Name == name {
			return spec.ResourceHash
		}
	}
	return ""
}

// This is a membership/intent proof, not a second ranking/filter algorithm:
// candidates choose original indices, while explicitly excluded domains cannot
// acquire source authority or become follow-up fetch targets.
func webSourceAllowed(raw string, filters []string) bool {
	if webcontent.ValidURL(raw) != nil {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	hasInclude, include := false, false
	for _, filter := range filters {
		deny := strings.HasPrefix(filter, "-")
		domain := strings.ToLower(strings.TrimPrefix(filter, "-"))
		match := host == domain || strings.HasSuffix(host, "."+domain)
		if deny && match {
			return false
		}
		if !deny {
			hasInclude = true
			include = include || match
		}
	}
	return !hasInclude || include
}
func webSourceReplySchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","maxLength":33554432},"sources":{"type":"array","items":{"type":"object","properties":{"url":{"type":"string","maxLength":33554432},"title":{"type":"string","maxLength":33554432}},"required":["url","title"],"additionalProperties":false}},"usage":{"type":"object","properties":{"input_tokens":{"type":"integer"},"output_tokens":{"type":"integer"},"cache_read_tokens":{"type":"integer"},"cache_write_tokens":{"type":"integer"}},"required":["input_tokens","output_tokens","cache_read_tokens","cache_write_tokens"],"additionalProperties":false},"retrieved_at":{"type":"string","maxLength":64},"error_code":{"type":"string","maxLength":128}},"required":["text","sources","retrieved_at","error_code"],"additionalProperties":false}`)
}
