package pricing

import (
	"math"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/llm"
)

func TestCatalogMatchesProviderChatModelAndTier(t *testing.T) {
	c := New()
	hash, _ := llm.EndpointHash("https://api.openai.com/v1")
	s := llm.Snapshot{ProviderKind: "openai_compatible", EndpointHash: hash, WireAPI: "openai_completions", ModelID: "gpt-6-luna"}
	quote, ok := c.Quote(s, llm.TokenUsage{InputTokens: 272000})
	// The same ID also exists as a classifier with zero output price. Its
	// rates must never replace the chat model's rates.
	if !ok || quote.Currency != "USD" || quote.Price.Input != 100000 || quote.Price.Output != 500000 || quote.Price.CacheRead != 10000 || quote.Price.CacheWrite != 125000 || !strings.Contains(quote.Source, "openai") || quote.Price.Version == "" {
		t.Fatal(quote, ok)
	}
	long, ok := c.Quote(s, llm.TokenUsage{InputTokens: 100000, CacheReadTokens: 172000, CacheWriteTokens: 1})
	if !ok || long.Price.Input != 200000 || long.Price.Output != 750000 || long.Price.CacheRead != 20000 || long.Price.CacheWrite != 250000 || long.Price.Version != quote.Price.Version {
		t.Fatal("long context tier", long, ok)
	}
	s.EndpointHash, _ = llm.EndpointHash("https://api.openai.com/v1/")
	if trailing, ok := c.Quote(s, llm.TokenUsage{}); !ok || trailing.Price != quote.Price {
		t.Fatal("equivalent endpoint", trailing, ok)
	}
	s.EndpointHash, _ = llm.EndpointHash("https://private-proxy.example/v1")
	if _, ok := c.Quote(s, llm.TokenUsage{}); ok {
		t.Fatal("guessed proxy prices from model name")
	}
	s.ProviderKind, s.ModelID = "openai_account", "gpt-6-luna"
	if reference, ok := c.Quote(s, llm.TokenUsage{}); !ok || !strings.Contains(reference.Source, "openai-codex") {
		t.Fatal("subscription reference quote", reference, ok)
	}
	s.ModelID = "private-unlisted-model"
	if _, ok := c.Quote(s, llm.TokenUsage{}); ok {
		t.Fatal("invented unknown model price")
	}
	s.ModelID = "gpt-6-luna"
	if _, ok := c.Quote(s, llm.TokenUsage{InputTokens: -1}); ok {
		t.Fatal("negative usage")
	}
}

func TestMicroRatePreservesDecimalPrices(t *testing.T) {
	for _, tt := range []struct {
		value float64
		want  int64
		valid bool
	}{
		{0, 0, true}, {.125, 125000, true}, {.000001, 1, true},
		{.0000001, 0, false}, {-1, 0, false}, {math.Inf(1), 0, false}, {math.NaN(), 0, false}, {1e20, 0, false},
	} {
		if got, valid := microRate(tt.value); got != tt.want || valid != tt.valid {
			t.Fatal(tt, got, valid)
		}
	}
}
