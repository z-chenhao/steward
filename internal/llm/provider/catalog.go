package provider

import (
	"embed"
	"encoding/json"
	"sort"
	"strings"
)

// Model metadata is transcribed from Pi AI 1.5.0's MIT-licensed published
// catalog. It is data only; see catalog/NOTICE.md for attribution.
//
//go:embed catalog/*.json
var catalogs embed.FS

func Providers() []string {
	files, _ := catalogs.ReadDir("catalog")
	out := []string{}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			out = append(out, strings.TrimSuffix(f.Name(), ".json"))
		}
	}
	sort.Strings(out)
	return out
}

func Models(provider string) []Model {
	if strings.ContainsAny(provider, "/\\.") {
		return nil
	}
	data, err := catalogs.ReadFile("catalog/" + provider + ".json")
	if err != nil {
		return nil
	}
	var groups map[string]map[string]Model
	if json.Unmarshal(data, &groups) != nil {
		return nil
	}
	out := []Model{}
	for _, group := range groups {
		for _, m := range group {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].API != out[j].API {
			return out[i].API < out[j].API
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func APIs() []API {
	return []API{OpenAICompletions, OpenAIResponses, AzureResponses, CodexResponses, AnthropicMessages, BedrockConverse, GoogleGenerative, GoogleVertex, MistralConversations, GatewayMessages}
}

func CalculateCost(m Model, u *Usage) {
	rates := m.Cost
	threshold := int64(-1)
	for _, tier := range m.Cost.Tiers {
		if u.Input+u.CacheRead+u.CacheWrite > tier.InputTokensAbove && tier.InputTokensAbove > threshold {
			rates = CostRates{Input: tier.Input, Output: tier.Output, CacheRead: tier.CacheRead, CacheWrite: tier.CacheWrite}
			threshold = tier.InputTokensAbove
		}
	}
	u.Cost = CostRates{Input: float64(u.Input) * rates.Input / 1e6, Output: float64(u.Output) * rates.Output / 1e6, CacheRead: float64(u.CacheRead) * rates.CacheRead / 1e6, CacheWrite: (float64(u.CacheWrite-u.CacheWrite1h)*rates.CacheWrite + float64(u.CacheWrite1h)*rates.Input*2) / 1e6}
	u.Cost.Total = u.Cost.Input + u.Cost.Output + u.Cost.CacheRead + u.Cost.CacheWrite
	u.TotalTokens = u.Input + u.Output + u.CacheRead + u.CacheWrite
}
