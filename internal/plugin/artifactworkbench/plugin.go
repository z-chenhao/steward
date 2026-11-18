package artifactworkbench

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.2", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Recipe: recipe, contract.Read: read, contract.Search: search, contract.BodyMatch: matchBody, contract.Cleanup: cleanup}}
}
func recipe(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.RecipeRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	plan, err := Recipe(request)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, plan)
}
func read(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.ReadRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, Read(request))
}

// ReadInputTo validates the complete immutable descriptor before any selection
// is released. Streaming through a pipe avoids buffering the catalogue again.
func consume(ctx context.Context, call rsiplugin.Call, id string, choose func(io.Reader) (contract.Selection, error)) (rsiplugin.Result, error) {
	if len(call.Inputs) != 1 || call.Inputs[0].ID != id {
		return rsiplugin.Result{}, errors.New("invalid metadata input")
	}
	input, output := io.Pipe()
	joined := make(chan error, 1)
	go func() {
		err := rsiplugin.ReadInputTo(ctx, call.Inputs[0], output)
		_ = output.CloseWithError(err)
		joined <- err
	}()
	plan, err := choose(bufio.NewReaderSize(input, 32<<10))
	_ = input.CloseWithError(err)
	streamErr := <-joined
	if err != nil {
		return rsiplugin.Result{}, err
	}
	if streamErr != nil {
		return rsiplugin.Result{}, streamErr
	}
	return rsiplugin.JSONResult(ctx, plan)
}
func search(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.SearchRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if _, valid := searchLimit(request); !valid {
		return rsiplugin.JSONResult(ctx, contract.Selection{Indices: []int{}, ErrorCode: "INVALID_ARGUMENT"})
	}
	return consume(ctx, call, contract.SearchInput, func(input io.Reader) (contract.Selection, error) { return Select(ctx, request, input) })
}
func cleanup(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.CleanupRequest
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if _, valid := cleanupLimit(request); !valid {
		return rsiplugin.JSONResult(ctx, contract.Selection{Indices: []int{}, ErrorCode: "INVALID_ARGUMENT"})
	}
	return consume(ctx, call, contract.CleanupInput, func(input io.Reader) (contract.Selection, error) { return SelectCleanup(ctx, request, input) })
}

func matchBody(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var q contract.BodyRequest
	if err := rsiplugin.Decode(call.Input, &q); err != nil {
		return rsiplugin.Result{}, err
	}
	if q.Query == "" || len(call.Inputs) != 1 || call.Inputs[0].ID != contract.BodyInput {
		return rsiplugin.Result{}, errors.New("invalid text search")
	}
	data, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contract.BodyResult{Match: bytes.Contains(data, []byte(q.Query))})
}
