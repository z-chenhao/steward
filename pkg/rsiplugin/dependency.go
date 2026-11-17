package rsiplugin

import (
	"context"
	"encoding/json"
	"errors"
)

type DependencyCall struct {
	Binding string          `json:"binding"`
	Key     string          `json:"key"`
	Input   json.RawMessage `json:"input"`
}
type dependencyClientKey struct{}
type dependencyClient func(context.Context, DependencyCall) (json.RawMessage, error)

// CallDependency can address only a named dependency in this instance's locked
// graph. Host supplies target identity, contract, scope and durable call key.
func CallDependency(ctx context.Context, binding, key string, input, output any) error {
	client, ok := ctx.Value(dependencyClientKey{}).(dependencyClient)
	if !ok {
		return errors.New("no dependency context")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	value, err := client(ctx, DependencyCall{Binding: binding, Key: key, Input: raw})
	if err != nil {
		return err
	}
	return Decode(value, output)
}
