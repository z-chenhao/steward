package builtin

import (
	_ "embed"
	"encoding/json"

	appsinterface "github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// The owner holds these captured native oracles and literal examples separately
// from all four candidate packages. No installed package or its output supplies
// an expected value. This is acceptance evidence, not runtime feature policy.
//
//go:embed apps-cases.json
var appsAcceptance []byte

func appsFeatureCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(appsAcceptance, &cases)
	return cases, err
}

// Reviewed client bytes are held by the independent owner, outside the installed
// package. Current and historical compositions select their declared protocol;
// a candidate never supplies its own expected worker or stylesheet.
//
//go:embed apps-client-owner-v3.json
var appsClientAcceptance []byte

func appsClientCases(cases []rsi.Case) ([]rsi.Case, error) {
	var view appsinterface.BrowserView
	if err := json.Unmarshal(appsClientAcceptance, &view); err != nil {
		return nil, err
	}
	for i := range cases {
		if cases[i].InstanceID != "apps-interface" || cases[i].Contract != appsinterface.LocalizedInterface {
			continue
		}
		var profile appsinterface.Profile
		if err := json.Unmarshal(cases[i].Expected, &profile); err != nil {
			return nil, err
		}
		cases[i].Contract = appsinterface.BrowserProfileInterface
		var err error
		cases[i].Expected, err = json.Marshal(appsinterface.BrowserProfile{Profile: profile, View: view})
		if err != nil {
			return nil, err
		}
	}
	return cases, nil
}

// Original independent-account expectations were captured before the candidate.
//
//go:embed apps-account-inventory-owner-v1.json
var appsAccountInventoryAcceptance []byte

func appsAccountInventoryCases() ([]rsi.Case, error) {
	var cases []rsi.Case
	err := json.Unmarshal(appsAccountInventoryAcceptance, &cases)
	return cases, err
}
