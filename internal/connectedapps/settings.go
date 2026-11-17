package connectedapps

import (
	"context"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
)

type SettingsReader func(context.Context) (SettingsPolicy, error)
type SettingsPolicy struct {
	mode accountPolicyMode
	auth AuthConfigPolicy
	read func(context.Context, int64, contract.SettingsRequest, []byte) (contract.SettingsPlan, error)
}
type SettingsRow struct{ App, Config string }
type SettingsSelection struct {
	State  State
	policy SettingsPolicy
}

func NativeSettings() SettingsPolicy {
	return SettingsPolicy{mode: accountPolicyNative, auth: NativeAuthConfig()}
}
func ManagedSettings(read func(context.Context, int64, contract.SettingsRequest, []byte) (contract.SettingsPlan, error)) SettingsPolicy {
	return SettingsPolicy{mode: accountPolicyManaged, read: read}
}
func NativeSettingsSelection(state State) SettingsSelection {
	return SettingsSelection{State: state, policy: NativeSettings()}
}
func (s *Service) Settings(ctx context.Context) (SettingsSelection, error) {
	state, err := s.State(ctx)
	if err != nil {
		return SettingsSelection{}, err
	}
	policy := NativeSettings()
	if s.ReadSettings != nil {
		policy, err = s.ReadSettings(ctx)
		if err != nil {
			return SettingsSelection{}, ErrUnavailable
		}
	}
	return SettingsSelection{State: state, policy: policy}, nil
}

// Rows accepts only a complete original-reference projection of the decoded
// owner command. Candidate cannot invent config values, drop unrelated rows,
// promote a draft to save or read credentials. Saved maps are not capped128.
func (s SettingsSelection) Rows(ctx context.Context, mode string, rows []SettingsRow, remove int) ([]SettingsRow, error) {
	if s.policy.mode == accountPolicyNative {
		return settingsRowsLegacyV1(mode, rows, remove), nil
	}
	if s.policy.mode != accountPolicyManaged || s.policy.read == nil {
		return nil, ErrUnavailable
	}
	count := len(rows)
	expected := count
	blank := false
	switch mode {
	case "saved", "keep":
		if count == 0 {
			expected = 1
			blank = true
		}
	case "add":
		if count < 1 || count >= 128 {
			return nil, ErrInvalid
		}
		expected++
		blank = true
	case "remove":
		if remove < 0 || remove >= count {
			return nil, ErrInvalid
		}
		expected--
		if expected == 0 {
			expected = 1
			blank = true
		}
	default:
		return nil, ErrInvalid
	}
	var data []byte
	if mode == "saved" {
		apps := make([]string, count)
		for i, row := range rows {
			apps[i] = row.App
		}
		data = contract.EncodeSettingsApps(apps)
	}
	seen := make([]bool, count)
	blankSeen := false
	result := make([]SettingsRow, 0, expected)
	for offset := 0; offset < expected; {
		plan, err := s.policy.read(ctx, s.State.Revision, contract.SettingsRequest{Mode: mode, Count: count, Offset: offset, Remove: remove}, data)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				return nil, ErrConflict
			}
			return nil, ErrUnavailable
		}
		n := min(contract.SettingsPageSize, expected-offset)
		if len(plan.Indices) != n {
			return nil, ErrUnavailable
		}
		for _, i := range plan.Indices {
			if i == -1 {
				if !blank || blankSeen {
					return nil, ErrUnavailable
				}
				blankSeen = true
				result = append(result, SettingsRow{})
				continue
			}
			if i < 0 || i >= count || seen[i] || mode == "remove" && i == remove {
				return nil, ErrUnavailable
			}
			seen[i] = true
			result = append(result, rows[i])
		}
		offset += n
	}
	if blankSeen != blank {
		return nil, ErrUnavailable
	}
	return result, nil
}

func (p SettingsPolicy) WithAuthConfig(auth AuthConfigPolicy) SettingsPolicy { p.auth = auth; return p }

// ConfigureContext retains the settings request's exact authentication-policy
// graph for the original explicit save; it does not authorize any new write.
func (s SettingsSelection) ConfigureContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, accountPoliciesKey{}, NativeAccountPolicies().WithAuthConfig(s.policy.auth))
}
