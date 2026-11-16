package contract

import (
	"reflect"
	"strings"
	"testing"
)

func TestAppsSettingsFactsExactBytesAndRejectMalformedInput(t *testing.T) {
	apps := []string{"", "private_\xff", "引用<>&", strings.Repeat("x", 3<<20)}
	raw := EncodeSettingsApps(apps)
	got, err := DecodeSettingsApps(raw, len(apps))
	if err != nil || !reflect.DeepEqual(got, apps) {
		t.Fatal("original app bytes normalized or capped", err)
	}
	for _, f := range []struct {
		raw   []byte
		count int
	}{{raw, -1}, {raw, len(apps) + 1}, {raw[:len(raw)-1], len(apps)}, {append(append([]byte{}, raw...), 0), len(apps)}} {
		if _, err := DecodeSettingsApps(f.raw, f.count); err == nil {
			t.Fatal("malformed settings input accepted")
		}
	}
}
