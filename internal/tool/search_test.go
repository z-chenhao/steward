package tool

import (
	"fmt"
	"testing"
)

func TestToolSearchRanksCapabilitiesWithoutExecutingOrGrantingAccess(t *testing.T) {
	specs := []Spec{}
	for i := 0; i < 500; i++ {
		specs = append(specs, Spec{Name: fmt.Sprintf("mcp__catalog__entry_%03d", i), Description: "Find a record in the catalog", Exposure: CodeMode})
	}
	specs = append(specs,
		Spec{Name: "mcp__calendar__list_events", Description: "List calendar events", Exposure: Deferred},
		Spec{Name: "mcp__calendar__create_event", Description: "Create calendar events", Exposure: CodeMode},
		Spec{Name: "mcp__private__list_events", Description: "List calendar events", Exposure: Hidden},
		Spec{Name: "builtin__list_events", Description: "List calendar events", Exposure: Direct},
		Spec{Name: "mcp__bridge__list_events", Description: "List calendar events", Exposure: Deferred, Internal: true},
	)
	result := SearchToolsLegacyV1(specs, nil, "calendar events", DefaultSearchLimit)
	if len(result.Loaded) != 2 {
		t.Fatal("wrong capability matches", result)
	}
	result = SearchToolsLegacyV1(specs, result.Loaded, "calendar events", DefaultSearchLimit)
	if len(result.Loaded) != 0 {
		t.Fatal("already active, direct or hidden tool was loaded", result)
	}
	if result := SearchToolsLegacyV1(specs, nil, "catalog", DefaultSearchLimit); len(result.Loaded) != 8 {
		t.Fatal("search loaded the whole catalog", len(result.Loaded))
	}
	if result := SearchToolsLegacyV1(specs, nil, "create_event", 1); len(result.Loaded) != 1 || result.Loaded[0] != "mcp__calendar__create_event" {
		t.Fatal("exact tool discovery failed", result)
	}
}

func TestToolSearchFindsSchemaAndNamespaceDescriptions(t *testing.T) {
	specs := []Spec{
		{Name: "mcp__service__lookup", Exposure: Deferred, InputSchema: `{"type":"object","properties":{"document":{"description":"contract renewal"}}}`},
		{Name: "mcp__finance__lookup", Exposure: CodeMode, NamespaceDescription: "Expense reimbursement"},
		{Name: "mcp__chinese__lookup", Exposure: CodeMode, Description: "查询日历事件"},
	}
	for _, test := range []struct{ query, name string }{{"contract renewal", specs[0].Name}, {"reimbursement", specs[1].Name}, {"日历", specs[2].Name}} {
		got := SearchToolsLegacyV1(specs, nil, test.query, 1)
		if len(got.Loaded) != 1 || got.Loaded[0] != test.name {
			t.Fatal(test.query, got)
		}
	}
}
