package rsi

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// ResourceFixture is authenticated owner evidence. Candidate verification uses
// these exact calls/results, never live credentials, network or model budgets.
type ResourceFixture struct {
	Contract string          `json:"contract"`
	Key      string          `json:"key"`
	Input    json.RawMessage `json:"input"`
	Output   json.RawMessage `json:"output"`
}

func verificationResources(fixtures []ResourceFixture) (*resourceScope, func() bool, error) {
	if len(fixtures) == 0 || len(fixtures) > 64 {
		return nil, nil, ErrInvalid
	}
	cursor := 0
	seen := map[string]bool{}
	ports := []Resource{}
	for _, fixture := range fixtures {
		if !validContract(fixture.Contract) || fixture.Key == "" || len(fixture.Key) > 128 || !json.Valid(fixture.Input) || !json.Valid(fixture.Output) {
			return nil, nil, ErrInvalid
		}
		if seen[fixture.Contract] {
			continue
		}
		seen[fixture.Contract] = true
		contract := fixture.Contract
		ports = append(ports, Resource{Contract: contract, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`), Invoke: func(_ context.Context, key string, input json.RawMessage) (json.RawMessage, error) {
			if cursor >= len(fixtures) {
				return nil, ErrInvalid
			}
			expected := fixtures[cursor]
			actual, err := rsiplugin.Canonical(input)
			if err != nil {
				return nil, err
			}
			want, err := rsiplugin.Canonical(expected.Input)
			if err != nil || expected.Contract != contract || expected.Key != key || !bytes.Equal(actual, want) {
				return nil, ErrInvalid
			}
			cursor++
			return bytes.Clone(expected.Output), nil
		}})
	}
	scope, err := newResourceScope(ports, func(c context.Context) error { return c.Err() })
	return scope, func() bool { return cursor == len(fixtures) && scope != nil && len(scope.pending) == 0 }, err
}
