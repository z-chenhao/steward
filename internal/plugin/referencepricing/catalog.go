// Package referencepricing owns the bundled reference-rate policy.
package referencepricing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/provider"
	"github.com/z-chenhao/assistant/internal/plugin/referencepricing/contract"
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

func (c *Catalog) Lookup(req contract.Request) *contract.Table {
	endpoint := req.EndpointHash
	switch req.ProviderKind {
	case "openai_account":
		endpoint = "account"
	case "openai_compatible":
		if endpoint == "" || (req.WireAPI != "openai_completions" && req.WireAPI != "openai_responses") {
			return nil
		}
	default:
		return nil
	}
	e, ok := c.models[key{endpoint, req.ModelID}]
	if !ok {
		return nil
	}
	convert := func(in provider.CostRates) (contract.Rates, bool) {
		values := []float64{in.Input, in.Output, in.CacheRead, in.CacheWrite}
		out := [4]int64{}
		for i, v := range values {
			n, ok := microRate(v)
			if !ok {
				return contract.Rates{}, false
			}
			out[i] = n
		}
		return contract.Rates{Input: out[0], Output: out[1], CacheRead: out[2], CacheWrite: out[3]}, true
	}
	base, ok := convert(e.model.Cost)
	if !ok {
		return nil
	}
	out := &contract.Table{Currency: "USD", Source: e.source, Version: e.version, Base: base, Tiers: []contract.Tier{}}
	seen := map[int64]bool{}
	for _, tier := range e.model.Cost.Tiers {
		if seen[tier.InputTokensAbove] {
			continue
		}
		seen[tier.InputTokensAbove] = true
		rates, ok := convert(provider.CostRates{Input: tier.Input, Output: tier.Output, CacheRead: tier.CacheRead, CacheWrite: tier.CacheWrite})
		if !ok {
			return nil
		}
		out.Tiers = append(out.Tiers, contract.Tier{Above: tier.InputTokensAbove, Rates: rates})
	}
	sort.Slice(out.Tiers, func(i, j int) bool { return out.Tiers[i].Above < out.Tiers[j].Above })
	return out
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
