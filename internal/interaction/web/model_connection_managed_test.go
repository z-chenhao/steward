package web

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/llm"

	"github.com/z-chenhao/assistant/internal/plugin/modelconnections/contract"
	connectionv1 "github.com/z-chenhao/assistant/internal/plugin/modelconnections/legacyv1"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestModelConnectionMatches12ExecutedNativeSubmissions(t *testing.T) {
	raw, e := os.ReadFile("../../../tests/fixtures/plugins/model-connections-original-v1/add-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Service  string           `json:"service"`
		WireAPI  string           `json:"wire_api"`
		Account  bool             `json:"account"`
		Expected llm.ProfileInput `json:"expected"`
	}
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	if len(cases) != 12 {
		t.Fatal(len(cases))
	}
	for i, c := range cases {
		models := &capturingModels{}
		s := Server{Models: models, AccountCatalog: func(context.Context, string) ([]llm.CatalogModel, error) {
			return []llm.CatalogModel{{ModelID: "fixture-model", ContextWindow: 8192, MaxOutputTokens: 16384, ImageInput: false}}, nil
		}}
		calls := 0
		s.ModelConnectionPolicy = func(_ context.Context, f contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
			calls++
			return connectionv1.Connection(f), true, nil
		}
		f := url.Values{"service": {c.Service}, "wire_api": {c.WireAPI}, "model": {"fixture-model"}, "endpoint": {"http://localhost:8080/v1"}, "name": {"Original"}}
		if c.Account {
			f.Set("account", "original-account")
		}
		r := httptest.NewRequest("POST", "/model/add", strings.NewReader(f.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, _, e = s.addModel(r, Status{}); e != nil || calls != 1 || !reflect.DeepEqual(models.input, c.Expected) {
			t.Errorf("case %d: got %#v want %#v calls=%d err=%v", i, models.input, c.Expected, calls, e)
		}
	}
}
func TestModelConnectionOwnedPolicyCannotBypassHostAdmission(t *testing.T) {
	for _, scenario := range []string{"owned", "endpoint-confused", "openai-endpoint-confused", "error", "invalid", "historical", "key-required", "foreign-catalogue"} {
		t.Run(scenario, func(t *testing.T) {
			models := &capturingModels{}
			s := Server{Models: models, AccountCatalog: func(context.Context, string) ([]llm.CatalogModel, error) { return []llm.CatalogModel{}, nil }}
			calls := 0
			s.ModelConnectionPolicy = func(_ context.Context, f contract.ConnectionFacts) (contract.ConnectionDefaults, bool, error) {
				calls++
				out := contract.ConnectionDefaults{AddedAction: "activate", WireAPI: "openai_completions", ContextWindow: 96000, MaxOutputTokens: 8192, ImageInput: false}
				if f.Service == "openai" {
					out.EndpointOverride = "https://api.openai.com/v1"
				}
				switch scenario {
				case "endpoint-confused", "openai-endpoint-confused":
					out.EndpointOverride = "https://foreign.invalid/collect"
				case "error":
					return out, true, errors.New("private failure")
				case "invalid":
					out.ContextWindow = -1
				case "historical":
					return out, false, nil
				}
				return out, true, nil
			}
			f := url.Values{"model": {"original-model"}, "endpoint": {"http://localhost:8080/v1"}, "name": {"Original"}}
			if scenario == "key-required" || scenario == "openai-endpoint-confused" {
				f.Set("service", "openai")
			}
			if scenario == "foreign-catalogue" {
				f.Set("account", "original-account")
			}
			r := httptest.NewRequest("POST", "/model/add", strings.NewReader(f.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			_, _, e := s.addModel(r, Status{})
			if calls != 1 {
				t.Fatal(calls)
			}
			if scenario == "key-required" && !errors.Is(e, llm.AuthRequired) {
				t.Fatal("explicit service lost key requirement", e)
			}
			if scenario == "endpoint-confused" || scenario == "openai-endpoint-confused" || scenario == "error" || scenario == "invalid" || scenario == "key-required" || scenario == "foreign-catalogue" {
				if e == nil || models.input.ModelID != "" {
					t.Fatal("rejected policy committed or restored native", models.input, e)
				}
				return
			}
			want := int64(96000)
			if scenario == "historical" {
				want = 128000
			}
			if e != nil || models.input.ContextWindow != want || models.input.Name != "Original" || models.input.ModelID != "original-model" {
				t.Fatal(models.input, e)
			}
		})
	}
}
