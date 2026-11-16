package web

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestAppsLocalizationActualV2MatchesOriginalHostDisplayText(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		t.Run(language, func(t *testing.T) {
			profileFile := "apps-interface-profile-native-v1.json"
			compositionFile := "apps-composition-order-native-v1.json"
			if language == "zh-CN" {
				profileFile = "apps-interface-profile-native-zh-v1.json"
				compositionFile = "apps-composition-native-zh-v1.json"
			}
			var profile contract.Profile
			var composition contract.Composition
			load := func(name string, value any) {
				t.Helper()
				raw, err := os.ReadFile("../../../tests/fixtures/plugins/" + name)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, value); err != nil {
					t.Fatal(err)
				}
			}
			load(profileFile, &profile)
			load(compositionFile, &composition)
			if actual := actualInterfaceProfileLocale(t, language); !reflect.DeepEqual(actual, profile) {
				t.Fatalf("actual v2 profile differs from original %s display\ngot=%#v\nwant=%#v", language, actual, profile)
			}
			if actual := actualAppCompositionLocale(t, language); !reflect.DeepEqual(actual, composition) {
				t.Fatalf("actual v2 composition differs from original %s display\ngot=%#v\nwant=%#v", language, actual, composition)
			}
		})
	}
}

func actualLocalizedAppPolicies(t *testing.T) map[string]AppInterfacePolicy {
	t.Helper()
	policies := map[string]AppInterfacePolicy{}
	var card contract.CardView
	var sidebar contract.SidebarView
	var markup contract.Markup
	if err := rsiplugin.DecodeResult(actualAppsPresentationResult(t, contract.CardViewInterface, json.RawMessage(`{}`)), &card); err != nil {
		t.Fatal(err)
	}
	if err := rsiplugin.DecodeResult(actualAppsPresentationResult(t, contract.SidebarViewInterface, json.RawMessage(`{}`)), &sidebar); err != nil {
		t.Fatal(err)
	}

	if err := rsiplugin.DecodeResult(actualAppsPresentationResult(t, contract.PagesMarkupInterface, json.RawMessage(`{}`)), &markup); err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "zh-CN"} {
		p, err := ManagedLocalizedAppInterface("fixture-original", actualInterfaceProfileLocale(t, locale))
		if err != nil {
			t.Fatal(err)
		}
		var composition contract.AccountComposition
		if err := rsiplugin.DecodeResult(actualAppsPresentationResult(t, contract.AccountCompositionInterface, json.RawMessage(`{"locale":"`+locale+`"}`)), &composition); err != nil {
			t.Fatal(err)
		}
		p, err = ManagedAppAccountComposition(p, composition)
		if err != nil {
			t.Fatal(err)
		}
		p, err = ManagedAppCardView(p, card)
		if err != nil {
			t.Fatal(err)
		}
		p, err = ManagedAppSidebarView(p, sidebar)
		if err != nil {
			t.Fatal(err)
		}
		p = actualCurrentAppMarkup(t, p, markup)
		policies[locale] = p
	}
	return policies
}

func actualCurrentAppMarkup(t *testing.T, p AppInterfacePolicy, m contract.Markup) AppInterfacePolicy {
	t.Helper()
	p, err := ManagedAppPagesMarkup(p, m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
