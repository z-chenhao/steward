package contract_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestCatalogueOrderFactsUseExactPrivateWireAndRejectCorruption(t *testing.T) {
	facts := []contract.OrderFact{{Name: "\x00[redacted]�汉", Slug: "abc"}, {}}
	wire := []byte{0, 253, 255, 230, 177, 137, 254, 97, 98, 99, 254, 254, 254}
	raw, err := contract.EncodeOrderFacts(facts)
	if err != nil || !bytes.Equal(raw, wire) {
		t.Fatal("wire bytes changed", raw, err)
	}
	restored, err := contract.DecodeOrderFacts(wire, 2)
	if err != nil || !reflect.DeepEqual(restored, facts) {
		t.Fatal("independent wire did not restore source", restored, err)
	}
	for _, bad := range [][]byte{{}, wire[:len(wire)-1], append(append([]byte{}, wire...), 254), {192, 254, 254}} {
		if _, err := contract.DecodeOrderFacts(bad, 2); err == nil {
			t.Fatal("invalid wire accepted")
		}
	}
	if data, err := contract.EncodeOrderFacts([]contract.OrderFact{{Name: string([]byte{254})}}); err == nil || data != nil {
		t.Fatal("invalid direct source text normalized silently", data, err)
	}
	if data, err := contract.EncodeOrderFacts([]contract.OrderFact{{Name: strings.Repeat("a", rsiplugin.MaxInputBytes)}}); err == nil || data != nil {
		t.Fatal("oversized source was truncated", len(data), err)
	}
}
func TestCatalogueOrderFactsPreserveCapturedNativeNormalizationBeyondInputSize(t *testing.T) {
	raw, err := os.ReadFile("../../../../tests/fixtures/plugins/apps-catalogue-normalized-input-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                                                string
		SourceBodyBytes, DecodedNameBytes, DecodedNameRunes int
		NameSHA256                                          string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			name := strings.Repeat("[redacted]", 1<<20)
			if c.Name == "invalid-utf8-expansion" {
				name = strings.Repeat(string(utf8.RuneError), 3<<20)
			}
			if len(name) != c.DecodedNameBytes || len(name) <= rsiplugin.MaxInputBytes {
				t.Fatal("fixture no longer covers original expansion", len(name))
			}
			encoded, err := contract.EncodeOrderFacts([]contract.OrderFact{{Name: name, Slug: "one"}, {Name: "A", Slug: "two"}})
			if err != nil || len(encoded) > c.SourceBodyBytes {
				t.Fatal("normalization exceeded original source budget", len(encoded), c.SourceBodyBytes, err)
			}
			decoded, err := contract.DecodeOrderFacts(encoded, 2)
			if err != nil || fmt.Sprintf("%x", sha256.Sum256([]byte(decoded[0].Name))) != c.NameSHA256 || utf8.RuneCountInString(decoded[0].Name) != c.DecodedNameRunes {
				t.Fatal("captured native bytes changed", err)
			}
		})
	}
}
