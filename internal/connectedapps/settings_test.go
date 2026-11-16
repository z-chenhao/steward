package connectedapps

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

func TestAppsSettingsHostPreservesOriginalRowsAndExplicitDraftAuthority(t *testing.T) {
	ctx := context.Background()
	rows := []SettingsRow{{App: "private_b", Config: "PRIVATE_CONFIG_B"}, {App: "private_a", Config: "PRIVATE_CONFIG_A"}}
	for _, f := range []struct {
		name, mode string
		remove     int
		indices    []int
	}{
		{"duplicate", "saved", -1, []int{0, 0}},
		{"foreign", "saved", -1, []int{0, 2}},
		{"invent-blank", "saved", -1, []int{0, -1}},
		{"missing", "saved", -1, []int{0}},
		{"drop-on-add", "add", -1, []int{0, -1}},
		{"double-blank-add", "add", -1, []int{0, -1, -1}},
		{"restore-removed", "remove", 0, []int{0}},
		{"drop-kept-row", "keep", -1, []int{1, 1}},
	} {
		t.Run(f.name, func(t *testing.T) {
			s := SettingsSelection{State: State{Revision: 7}, policy: ManagedSettings(func(_ context.Context, revision int64, r contract.SettingsRequest, data []byte) (contract.SettingsPlan, error) {
				if revision != 7 || r.Count != 2 || r.Offset != 0 {
					t.Fatal("lost exact original scope", revision, r)
				}
				if f.mode == "saved" {
					apps, err := contract.DecodeSettingsApps(data, 2)
					if err != nil || !reflect.DeepEqual(apps, []string{"private_b", "private_a"}) {
						t.Fatal("projection included config values or lost app facts", apps, err)
					}
				} else if len(data) != 0 {
					t.Fatal("draft sent private saved facts")
				}
				return contract.SettingsPlan{Indices: f.indices}, nil
			})}
			if got, err := s.Rows(ctx, f.mode, rows, f.remove); got != nil || !errors.Is(err, ErrUnavailable) {
				t.Fatal("invalid draft released partial/invented rows", got, err)
			}
		})
	}
	s := SettingsSelection{State: State{Revision: 7}, policy: ManagedSettings(func(context.Context, int64, contract.SettingsRequest, []byte) (contract.SettingsPlan, error) {
		return contract.SettingsPlan{Indices: []int{-1, 1, 0}}, nil
	})}
	got, err := s.Rows(ctx, "add", rows, -1)
	if err != nil || !reflect.DeepEqual(got, []SettingsRow{{}, rows[1], rows[0]}) {
		t.Fatal("valid independent projection changed original values", got, err)
	}
	s.policy = SettingsPolicy{}
	if _, err := s.Rows(ctx, "saved", rows, -1); !errors.Is(err, ErrUnavailable) {
		t.Fatal("zero policy inferred native", err)
	}
}
