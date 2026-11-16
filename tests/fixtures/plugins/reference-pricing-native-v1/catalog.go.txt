// Package pricing matches private Host reference estimates to the bundled
// model catalog. Quotes are USD API list prices, never provider invoices.
package pricing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/provider"
)

type key struct{ endpoint, model string }
type entry struct {
	model           provider.Model
	version, source string
}
type Catalog struct{ models map[key]entry }

func New() *Catalog {
	c := &Catalog{models: map[key]entry{}}
	ambiguous := map[key]bool{}
	for _, name := range provider.Providers() {
		for _, m := range provider.Models(name) {
			if m.Type != "chat" || (m.API != provider.OpenAICompletions && m.API != provider.OpenAIResponses && m.API != provider.CodexResponses) {
				continue
			}
			hash, err := llm.EndpointHash(strings.TrimRight(m.BaseURL, "/"))
			if err != nil || hash == "" {
				continue
			}
			if name == "openai-codex" {
				hash = "account" // Subscription reference rates; no endpoint in its snapshot.
			}
			k := key{hash, m.ID}
			data, _ := json.Marshal(m.Cost)
			if old, exists := c.models[k]; exists {
				previous, _ := json.Marshal(old.model.Cost)
				if string(data) != string(previous) {
					ambiguous[k] = true
				}
				continue
			}
			sum := sha256.Sum256(data)
			c.models[k] = entry{m, "catalog-" + hex.EncodeToString(sum[:8]), "Bundled catalog (Pi AI 1.5.0) / " + name}
		}
	}
	for k := range ambiguous {
		delete(c.models, k)
	}
	aliases := map[key]entry{}
	for k, e := range c.models {
		if k.endpoint != "account" {
			hash, _ := llm.EndpointHash(strings.TrimRight(e.model.BaseURL, "/") + "/")
			aliases[key{hash, k.model}] = e
		}
	}
	for k, e := range aliases {
		c.models[k] = e
	}
	return c
}

func (c *Catalog) Quote(snapshot llm.Snapshot, usage llm.TokenUsage) (agent.ReferencePrice, bool) {
	endpoint := snapshot.EndpointHash
	switch snapshot.ProviderKind {
	case "openai_account":
		endpoint = "account"
	case "openai_compatible":
		if endpoint == "" || (snapshot.WireAPI != "openai_completions" && snapshot.WireAPI != "openai_responses") {
			return agent.ReferencePrice{}, false
		}
	default:
		return agent.ReferencePrice{}, false
	}
	e, ok := c.models[key{endpoint, snapshot.ModelID}]
	if !ok || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheWriteTokens < 0 {
		return agent.ReferencePrice{}, false
	}
	// Compare without overflowing the sum of disjoint input buckets.
	input := new(big.Int).Add(big.NewInt(usage.InputTokens), big.NewInt(usage.CacheReadTokens))
	input.Add(input, big.NewInt(usage.CacheWriteTokens))
	rates := e.model.Cost
	threshold := int64(-1)
	for _, tier := range rates.Tiers {
		if input.Cmp(big.NewInt(tier.InputTokensAbove)) > 0 && tier.InputTokensAbove > threshold {
			rates = provider.CostRates{Input: tier.Input, Output: tier.Output, CacheRead: tier.CacheRead, CacheWrite: tier.CacheWrite}
			threshold = tier.InputTokensAbove
		}
	}
	values := []float64{rates.Input, rates.Output, rates.CacheRead, rates.CacheWrite}
	converted := [4]int64{}
	for i, value := range values {
		rate, valid := microRate(value)
		if !valid {
			return agent.ReferencePrice{}, false
		}
		converted[i] = rate
	}
	return agent.ReferencePrice{Currency: "USD", Source: e.source, Price: agent.ModelPrice{
		Model: snapshot.ModelID, Version: e.version, Input: converted[0], Output: converted[1], CacheRead: converted[2], CacheWrite: converted[3],
	}}, true
}

func microRate(value float64) (int64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'f', -1, 64))
	if !ok {
		return 0, false
	}
	r.Mul(r, big.NewRat(1000000, 1))
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	return r.Num().Int64(), true
}
