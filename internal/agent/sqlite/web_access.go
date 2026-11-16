package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/z-chenhao/assistant/internal/rsi"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

func (r *Runs) ConfigureWebAccess(c webaccess.Config, p llm.LLMProvider, secrets ...llm.SecretStore) {
	r.webAccess = c
	r.webSourceRead = nil
	if len(secrets) > 0 {
		r.webSecrets = secrets[0]
	}
	r.webFetch = webcontent.Fetch
	r.webContentFetch = webcontent.FetchContent
	r.webProvider = nil
	if p != nil {
		r.webProvider = r.GuardProvider(p)
	}
}

func (t *tools) executeWebAccess(ctx context.Context, inv tool.Invocation) (tool.Result, error) {
	var name, args string
	err := t.runs.write(ctx, func(tx *sql.Tx) error {
		_, n, a, e := t.acceptedBuiltin(ctx, tx, inv)
		name, args = n, a
		return e
	})
	if err != nil {
		return tool.Result{}, err
	}
	run, err := t.runs.Load(ctx, t.claim)
	if err != nil {
		return tool.Result{}, err
	}
	matched := false
	for _, s := range run.Tools {
		if s.Name == name && s.ResourceHash == t.runs.webResourceIdentity(run, name) {
			matched = true
		}
	}
	if !matched {
		return tool.Failure("MODEL_INCOMPATIBLE"), nil
	}
	if name == webaccess.FetchName {
		managed, e := t.runs.managedRunService(ctx, t.claim.RunID(), webaccess.FetchContract)
		if e != nil {
			return tool.Result{}, e
		}
		if managed {
			return t.executeWebFetch(ctx, inv, run, args)
		}
	}
	if name == webaccess.ReadName {
		return t.executeWebRead(ctx, inv, run, args)
	}
	if name == webaccess.SearchName || name == webaccess.ContentName || name == webaccess.CheckName {
		managed, e := t.runs.managedRunService(ctx, t.claim.RunID(), webaccess.ResearchContract)
		if e != nil {
			return tool.Result{}, e
		}
		if managed {
			return t.executeWebResearch(ctx, inv, run, name, args)
		}
	}
	plugin := webaccess.Plugin{Config: t.runs.webAccess, NativeSearch: run.Snapshot.ProviderKind == "openai_account", ContextWindow: run.Snapshot.ContextWindow, Secrets: t.runs.webSecrets, FetchContent: t.runs.webContentFetch, Fetch: t.runs.webFetch}
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), webaccess.SearchContract)
	if err != nil {
		return tool.Result{}, err
	}
	if managed {
		plugin.Search = func(ctx context.Context, index int, q webaccess.SearchQuery) (webaccess.SearchResult, error) {
			return t.managedWebSearch(ctx, inv, run, index, q)
		}
	} else {
		// Only frozen pre-service Runs and explicit unbundled carriers use this
		// historical method. Source IO has one Host-owned adapter.
		plugin.Search = func(c context.Context, index int, q webaccess.SearchQuery) (webaccess.SearchResult, error) {
			var hosted webaccess.QuerySearch
			if run.Snapshot.ProviderKind == "openai_account" {
				hosted = func(c context.Context, q webaccess.SearchQuery) (llm.GenerationResult, error) {
					return t.webModelGeneration(c, inv, run, fmt.Sprintf("search:%d", index), webaccess.ModelCall{Kind: "hosted_search", Query: &q})
				}
			}
			return webaccess.LegacySearch(c, t.runs.webAccess, q, func(c context.Context, source string, q webaccess.SearchQuery) (webaccess.SourceObservation, error) {
				facts, err := t.runs.readWebSource(c, source, q, hosted)
				if err != nil {
					return facts, err
				}
				return webaccess.LegacyFormatSourceV2(facts)
			})
		}
	}
	result, err := plugin.Execute(ctx, webAccessHost{tools: t, invocation: inv, run: run}, name, args)
	if errors.Is(err, webaccess.ErrSearchMethod) {
		if authority := t.runs.evidenceReadTx(ctx, func(tx *sql.Tx) error { return t.fence(ctx, tx) }); authority != nil {
			return tool.Result{}, authority
		}
		if errors.Is(err, errWebSearchNoSourceDispatch) {
			return tool.Failure("PROVIDER_UNAVAILABLE"), nil
		}
		if errors.Is(err, rsi.ErrUnknown) {
			return tool.Failure("UNKNOWN_SIDE_EFFECT"), nil
		}
		return tool.Failure("PROVIDER_UNAVAILABLE"), nil
	}
	return result, err
}

// Both retained native work and managed source ports use the same concrete
// owner adapter. Tests can replace this private IO port without global HTTP
// mutation; configuration reload clears that binding.
func (r *Runs) readWebSource(ctx context.Context, source string, q webaccess.SearchQuery, hosted webaccess.QuerySearch) (webaccess.SourceObservation, error) {
	if r.webSourceRead != nil {
		return r.webSourceRead(ctx, source, q, hosted)
	}
	return webaccess.ReadSourceFacts(ctx, r.webAccess, source, q, hosted, r.webSecrets, nil)
}
