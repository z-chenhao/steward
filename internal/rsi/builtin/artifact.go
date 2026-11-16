package builtin

import (
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

// Independent owner literals; the producer cannot supply its own oracle.
func artifactCases() []rsi.Case {
	return []rsi.Case{
		{InstanceID: "artifact-workbench", Contract: contract.Recipe, Input: json.RawMessage(`{"retention_days":0}`), Expected: json.RawMessage(`{"retention_days":30,"media_kinds":["image","audio","video"],"overflow_name":"tool-output.txt","overflow_media":"text/plain","stored_text":"Saved artifact.","publish_text":"Final artifacts selected."}`)},
		{InstanceID: "artifact-workbench", Contract: contract.Read, Input: json.RawMessage(`{"media_type":"text/markdown","byte_size":21,"offset":0,"limit":0}`), Expected: json.RawMessage(`{"kind":"text","limit":4096}`)},
		{InstanceID: "artifact-workbench", Contract: contract.Search, Input: json.RawMessage(`{"count":3,"query":" α ","limit":1}`), Inputs: []rsi.InputData{{ID: contract.SearchInput, Data: contract.EncodeSearch([]contract.SearchRow{{CreatedAt: 30, Name: "Α report", MediaType: "text/plain"}, {CreatedAt: 50, Name: "other", MediaType: "application/json"}, {CreatedAt: 2, Name: "α older", MediaType: "text/plain"}})}}, Expected: json.RawMessage(`{"indices":[0],"limit":1}`)},
		{InstanceID: "artifact-workbench", Contract: contract.Cleanup, Input: json.RawMessage(`{"count":3,"batch":2}`), Inputs: []rsi.InputData{{ID: contract.CleanupInput, Data: contract.EncodeCleanup([]contract.CleanupRow{{ExpiresAt: 30}, {ExpiresAt: 2}, {ExpiresAt: 4}})}}, Expected: json.RawMessage(`{"indices":[1,2],"limit":2}`)},
	}
}
