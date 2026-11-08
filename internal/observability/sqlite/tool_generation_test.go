package sqlite

import (
	"database/sql"
	"encoding/json"
	"testing"
)

func TestToolGenerationsKeepTheirOwningToolAndPurpose(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE members(run_id TEXT);INSERT INTO members VALUES('own');
 CREATE TABLE model_generation(generation_id TEXT,run_id TEXT,step_index INTEGER,status TEXT,created_at INTEGER,completed_at INTEGER,error_code TEXT,request_json TEXT);
 CREATE TABLE context_compaction(run_id TEXT,step_index INTEGER,provenance_json TEXT);
 CREATE TABLE tool_invocation(invocation_id TEXT,run_id TEXT);INSERT INTO tool_invocation VALUES('local','own'),('foreign','other');`)
	if err != nil {
		t.Fatal(err)
	}
	for index, tc := range []struct {
		kind, owner, name, parent string
		legacy, summary           bool
	}{
		{kind: "hosted_search", owner: "local", name: "Hosted web search", parent: "tool:local"},
		{kind: "summary", owner: "local", name: "Summarize search results", parent: "tool:local"},
		{kind: "page_answer", owner: "local", name: "Answer from page", parent: "tool:local"},
		{owner: "local", name: "Hosted web search", parent: "tool:local", legacy: true},
		{kind: "hosted_search", owner: "foreign", name: "Hosted web search", parent: "agent:own"},
		{name: "Model call", parent: "agent:own"},
		{name: "Summarize context", parent: "agent:own", summary: true},
	} {
		t.Run(tc.name+tc.owner, func(t *testing.T) {
			request := map[string]any{"messages": []any{map[string]any{"text": "Search this public query using hosted web search"}}}
			if tc.legacy {
				request["web_search"] = map[string]any{"invocation_id": tc.owner}
			} else if tc.kind != "" {
				request["tool_generation"] = map[string]any{"kind": tc.kind, "invocation_id": tc.owner}
			}
			raw, _ := json.Marshal(request)
			if _, err = db.Exec(`INSERT INTO model_generation VALUES(?,'own',?,'completed',1,2,'',?)`, index, index, string(raw)); err != nil {
				t.Fatal(err)
			}
			if tc.summary {
				if _, err = db.Exec(`INSERT INTO context_compaction VALUES('own',?,'{"stage":"summary_request"}')`, index); err != nil {
					t.Fatal(err)
				}
			}
			node, err := scanObservation(db.QueryRow(observationSources[1]+` WHERE g.generation_id=?`, index))
			if err != nil || node.Name != tc.name || node.Parent != tc.parent {
				t.Fatal(node, err)
			}
			if node.Type != "generation" || node.Status != "completed" {
				t.Fatal("reclassified billable generation as non-model event", node)
			}
			if tc.parent == "tool:local" && node.Detail == "" {
				t.Fatal("missing distinction from an Agent turn")
			}
		})
	}
}
