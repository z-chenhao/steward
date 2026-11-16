package appsdirectory

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed editorial.json
var editorialData []byte

func Handlers() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: "steward.apps-directory", Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Editorial: editorial, contract.CategoryOptions: categories, contract.Captions: captions, contract.CatalogueOrder: catalogueOrder, contract.CardStates: cardStates, contract.InventoryOrder: inventoryOrder}}
}

func editorial(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var input contract.Query
	if err := rsiplugin.Decode(call.Input, &input); err != nil {
		return rsiplugin.Result{}, err
	}
	var value contract.Catalogue
	if err := json.Unmarshal(editorialData, &value); err != nil {
		return rsiplugin.Result{}, err
	}
	value.Discovery = discovery(value.Items)
	value.Filter = filter(value.Items, input)
	value.Route = route(input)
	value.Lookup = lookup(input)
	value.Labels = labels()
	return rsiplugin.JSONResult(ctx, value)
}
