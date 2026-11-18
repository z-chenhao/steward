package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type ownerInventory struct{ Instances, Cases []string }

func readDefaultOwnerInventory(t *testing.T) ownerInventory {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "plugins", "default-owner-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected ownerInventory
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	return expected
}

func assertDefaultOwnerInventory(t *testing.T, bindings []rsiplugin.Binding, policyJSON string) {
	t.Helper()
	expected := readDefaultOwnerInventory(t)
	var policy struct{ Cases []map[string]any }
	decoder := json.NewDecoder(bytes.NewBufferString(policyJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&policy); err != nil {
		t.Fatal(err)
	}
	instances := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		instances = append(instances, binding.InstanceID)
	}
	cases := make([]string, 0, len(policy.Cases))
	for _, request := range policy.Cases {
		contract, ok := request["contract"]
		if !ok {
			contract = "hook"
		}
		delete(request, "expected")
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(request); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
		cases = append(cases, fmt.Sprintf("%s/%s %x", request["instance_id"], contract, digest))
	}
	compareOwnerInventory(t, "instances", expected.Instances, instances)
	compareOwnerInventory(t, "cases", expected.Cases, cases)
}

func compareOwnerInventory(t *testing.T, kind string, expected, actual []string) {
	t.Helper()
	if len(expected) == 0 {
		t.Fatalf("reviewed default Owner %s inventory is empty", kind)
	}
	slices.Sort(actual)
	if slices.Equal(expected, actual) {
		return
	}
	unmatched := make(map[string]int)
	for _, key := range expected {
		unmatched[key]--
	}
	for _, key := range actual {
		unmatched[key]++
	}
	for key, difference := range unmatched {
		if difference != 0 {
			t.Errorf("default Owner %s differ for %s (actual minus expected: %d)", kind, key, difference)
		}
	}
}
