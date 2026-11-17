package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/appsmethods/contract"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/tool/codemode"
)

func TestConnectedAppsPluginActualPagePolicyUpgradeKeepsOpaqueCursorWithoutDropOrReplay(t *testing.T) {
	f := newAppsPluginFixture(t)
	const query = "private exact query + & 中文"
	type pagePosition struct{ offset, size int }
	var mutex sync.Mutex
	cursors := map[string]pagePosition{}
	var observed []pagePosition
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != "GET" || request.URL.Path != "/tools" || request.Header.Get("x-api-key") != "composio-project-private" || request.URL.Query().Get("query") != query || request.URL.Query().Get("toolkit_slug") != "gmail" || request.URL.Query().Get("include_deprecated") != "false" || request.URL.Query().Get("toolkit_versions") != "latest" {
			t.Error("page policy changed the original closed search authority", request.Method, request.URL)
			http.Error(w, "invalid request", 400)
			return
		}
		mutex.Lock()
		defer mutex.Unlock()
		position := pagePosition{}
		if cursor := request.URL.Query().Get("cursor"); cursor != "" {
			var valid bool
			position, valid = cursors[cursor]
			if !valid || request.URL.Query().Has("limit") {
				t.Error("continuation changed an opaque cursor's original page size", request.URL)
				http.Error(w, "invalid cursor", 400)
				return
			}
		} else {
			var err error
			position.size, err = strconv.Atoi(request.URL.Query().Get("limit"))
			if err != nil || position.size != 4 && position.size != 8 {
				t.Error("actual first page did not use its frozen method's recipe", request.URL)
				http.Error(w, "invalid size", 400)
				return
			}
		}
		observed = append(observed, position)
		var items []composio.Tool
		end := min(position.offset+position.size, 19)
		for index := position.offset; index < end; index++ {
			item := f.peer.tool
			item.Slug = fmt.Sprintf("GMAIL_OPERATION_%02d", index)
			items = append(items, item)
		}
		next := ""
		if end < 19 {
			// The provider fixture alone interprets tokens. Neither Host nor
			// candidate decodes or regenerates any upstream cursor.
			next = fmt.Sprintf("opaque.%d+%d=&界", len(cursors), end)
			cursors[next] = pagePosition{end, position.size}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "next_cursor": next})
	}))
	t.Cleanup(server.Close)
	f.peer.service.Client.Endpoint = server.URL
	var currentCursor string
	f.provider.respond = delegateToolWork(func(request llm.GenerationRequest) (llm.GenerationResult, error) {
		if request.Messages[len(request.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "Original catalogue page observed."}, nil
		}
		arguments, _ := json.Marshal(map[string]string{"query": query, "cursor": currentCursor})
		args, _ := json.Marshal(map[string]string{"source": `return await tools.` + f.prefix + `search_tools(` + string(arguments) + `);`})
		return calls(codemode.ToolName, string(args)), nil
	})
	sequence := 0
	queue := func(cursor string) string {
		t.Helper()
		sequence++
		currentCursor = cursor
		receipt := mustSubmit(t, f.actor, fmt.Sprintf("apps-page-%d", sequence), "Read the next page of the original connected account")
		if err := executeTaskRunID(t, f.actor, f.runs, receipt.RunID); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := f.store.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND status='queued' ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	type item struct {
		composio.Tool
		Hash string `json:"descriptor_hash"`
	}
	type page struct {
		Items []item `json:"items"`
		Next  string `json:"next_cursor"`
	}
	read := func(id, graph string, wantCount int) page {
		t.Helper()
		mustDrain(t, f.actor)
		var raw, selected string
		if err := f.store.db.QueryRow(`SELECT i.result_json,p.graph_revision FROM tool_invocation i JOIN rsi_run p USING(run_id) WHERE i.run_id=? AND i.tool_name=? AND i.parent_invocation_id IS NOT NULL`, id, f.prefix+"search_tools").Scan(&raw, &selected); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var found page
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &found) != nil || len(found.Items) != wantCount || selected != graph {
			t.Fatal("actual nested page lost its frozen graph or complete original result", raw, selected, graph)
		}
		for _, item := range found.Items {
			original := f.peer.tool
			original.Slug = item.Slug
			if !reflect.DeepEqual(item.Tool, original) || item.Hash == "" {
				t.Fatal("page recipe altered an original descriptor", item)
			}
		}
		return found
	}
	first, _ := selectAppsVersion(t, f.host, "", "1.0.0")
	queued := queue("")
	second, _ := selectAppsVersion(t, f.host, first, "1.6.0")
	old := read(queued, first, 8)
	allOld := append([]item{}, old.Items...)
	for _, wantCount := range []int{8, 3} {
		old = read(queue(old.Next), second, wantCount)
		allOld = append(allOld, old.Items...)
	}
	if old.Next != "" {
		t.Fatal("old cursor did not complete on the exact last page", old.Next)
	}
	fresh := read(queue(""), second, 4)
	allNew := append([]item{}, fresh.Items...)
	activateDelegation(t, f.host, "apps-page-rollback", second, first, "apps-owner-1.0.0")
	for _, wantCount := range []int{4, 4, 4, 3} {
		fresh = read(queue(fresh.Next), first, wantCount)
		allNew = append(allNew, fresh.Items...)
	}
	if fresh.Next != "" || !reflect.DeepEqual(allOld, allNew) || len(allOld) != 19 {
		t.Fatal("opaque continuation dropped, replayed or reordered a descriptor", len(allOld), len(allNew), fresh.Next)
	}
	for index, item := range allOld {
		if item.Slug != fmt.Sprintf("GMAIL_OPERATION_%02d", index) {
			t.Fatal("pagination did not cover every original operation exactly once", index, item.Slug)
		}
	}
	mutex.Lock()
	want := []pagePosition{{0, 8}, {8, 8}, {16, 8}, {0, 4}, {4, 4}, {8, 4}, {12, 4}, {16, 4}}
	got := append([]pagePosition{}, observed...)
	mutex.Unlock()
	if !reflect.DeepEqual(got, want) || scalar(t, f.store, `SELECT count(*) FROM tool_invocation WHERE tool_name=? AND status='succeeded'`, f.prefix+"search_tools") != 8 || scalar(t, f.store, `SELECT count(*) FROM rsi_call`) != 0 || f.peer.executions.Load() != 0 {
		t.Fatal("actual page requests repeated or bypassed original Tool receipts", got)
	}
}

func TestConnectedAppsMethodPagePortRejectsInvalidRecipeBeforeOriginalIO(t *testing.T) {
	f := newAppsPluginFixture(t)
	var ioCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		ioCalls.Add(1)
		http.Error(w, "unexpected source dispatch", 500)
	}))
	t.Cleanup(server.Close)
	f.peer.service.Client.Endpoint = server.URL
	state, err := f.store.LoadApps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hash := connectedapps.BindingHash(state, f.connection)
	for _, scenario := range []struct {
		cursor string
		phase  string
		limit  int
	}{{"", "search", -1}, {"", "search", 0}, {"", "search", 1001}, {"original+opaque", "search", 4}, {"", "binding", 8}, {"", "execute", 8}} {
		ctx := connectedapps.WithMethod(context.Background(), func(ctx context.Context, _, _ string, _ contract.Request, source func(context.Context, string, contract.SourceRequest) (contract.SourceReply, error)) (contract.Plan, error) {
			if scenario.phase != "binding" {
				if _, err := source(ctx, "binding", contract.SourceRequest{Phase: "binding"}); err != nil {
					return contract.Plan{}, err
				}
			}
			_, err := source(ctx, scenario.phase, contract.SourceRequest{Phase: scenario.phase, Limit: scenario.limit})
			return contract.Plan{}, err
		})
		arguments, _ := json.Marshal(map[string]string{"query": "original query", "cursor": scenario.cursor})
		result, err := f.peer.service.Method(ctx, f.connection.ID, hash, f.prefix+"search_tools", string(arguments))
		if !errors.Is(err, connectedapps.ErrInvalid) && !errors.Is(err, connectedapps.ErrDenied) || len(result.JSON) != 0 || result.Ambiguous {
			t.Fatal("invalid source recipe did not fail before original IO", scenario, result, err)
		}
	}
	if ioCalls.Load() != 0 {
		t.Fatal("invalid method recipe reached original source IO", ioCalls.Load())
	}
}
