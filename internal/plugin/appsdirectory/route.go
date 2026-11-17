package appsdirectory

import "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"

func route(query contract.Query) *contract.Route {
	if !query.LiveConfigured {
		return &contract.Route{Source: "editorial"}
	}
	size := 40
	if query.HasCursor {
		size = 0
	}
	return &contract.Route{Source: "live", PageSize: size, OnLiveError: "unavailable"}
}
