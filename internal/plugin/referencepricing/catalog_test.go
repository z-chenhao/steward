package referencepricing

import (
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/referencepricing/contract"
)

func TestOriginalExecutedQuotesKeepExactTierAndSource(t *testing.T) {
	var cases []struct {
		Snapshot llm.Snapshot
		Usage    llm.TokenUsage
		Quote    agent.ReferencePrice
		Known    bool
	}
	raw, err := os.ReadFile("../../../tests/fixtures/plugins/reference-pricing-native-v1/quotes.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	c := New()
	for i, test := range cases {
		s, u := test.Snapshot, test.Usage
		table := c.Lookup(contract.Request{ProviderKind: s.ProviderKind, EndpointHash: s.EndpointHash, WireAPI: s.WireAPI, ModelID: s.ModelID})
		known := table != nil && u.InputTokens >= 0 && u.OutputTokens >= 0 && u.CacheReadTokens >= 0 && u.CacheWriteTokens >= 0
		q := agent.ReferencePrice{}
		if known {
			rates := table.Base
			total := new(big.Int).Add(big.NewInt(u.InputTokens), big.NewInt(u.CacheReadTokens))
			total.Add(total, big.NewInt(u.CacheWriteTokens))
			for _, tier := range table.Tiers {
				if total.Cmp(big.NewInt(tier.Above)) > 0 {
					rates = tier.Rates
				}
			}
			q = agent.ReferencePrice{Currency: table.Currency, Source: table.Source, Price: agent.ModelPrice{Model: s.ModelID, Version: table.Version, Input: rates.Input, Output: rates.Output, CacheRead: rates.CacheRead, CacheWrite: rates.CacheWrite}}
		}
		if known != test.Known || !reflect.DeepEqual(q, test.Quote) {
			t.Fatalf("original case %d: %#v/%v expected %#v/%v", i, q, known, test.Quote, test.Known)
		}
	}
}
