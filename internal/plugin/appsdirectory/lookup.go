package appsdirectory

import "github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"

func lookup(query contract.Query) *contract.Lookup {
	return &contract.Lookup{PreferLive: query.LiveConfigured, LocalFirst: "editorial", OnLiveError: "local"}
}
