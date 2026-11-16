package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Execute the actual guard with closed synthetic identity facts. In particular,
// a NULL in a corrupt parent catalog must not grant an unmatched capability.
func TestChildStorageMembershipGuardExactDefinitionsAndNulls(t *testing.T) {
	first := `{"name":"mcp__catalog__first","permission":"read_only","version":1,"description":"original"}`
	second := `{"name":"mcp__catalog__second","permission":"read_only","version":1}`
	changed := `{"name":"mcp__catalog__first","permission":"read_only","version":1,"description":"changed"}`
	reordered := `{"permission":"read_only","name":"mcp__catalog__first","version":1,"description":"original"}`
	for _, tc := range []struct {
		name, parent, child string
		allowed             bool
	}{
		{"same definitions", `[` + first + `,` + second + `]`, `[` + first + `,` + second + `]`, true},
		{"proper subset", `[` + first + `,` + second + `]`, `[` + second + `]`, true},
		{"empty subset", `[` + first + `]`, `[]`, true},
		{"same name changed descriptor", `[` + first + `]`, `[` + changed + `]`, false},
		{"same value reordered encoding", `[` + first + `]`, `[` + reordered + `]`, false},
		{"foreign capability", `[` + first + `]`, `[` + second + `]`, false},
		{"NULL child", `[` + first + `]`, `[null]`, false},
		{"NULL never matches NULL", `[null,` + first + `]`, `[null]`, false},
		{"parent NULL cannot grant foreign", `[null,` + first + `]`, `[` + second + `]`, false},
		{"parent NULL preserves original grant", `[null,` + first + `]`, `[` + first + `]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "guard.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, err = db.Exec(`CREATE TABLE capability_snapshot(snapshot_id TEXT PRIMARY KEY,tools_json TEXT,can_spawn_agents INTEGER,role TEXT,data_scope TEXT,agent_config_version INTEGER,format_version INTEGER);
  CREATE TABLE run(run_id TEXT PRIMARY KEY,capability_snapshot_id TEXT,worker_position TEXT,parent_run_id TEXT,kind TEXT,status TEXT,desired_state TEXT,task_id TEXT,root_run_id TEXT,task_epoch INTEGER,agent_id TEXT,conversation_id TEXT,depth INTEGER,model_snapshot_id TEXT);` + childMembershipSQL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO capability_snapshot VALUES('pc',?,1,'worker','scope',1,1),('cc',?,1,'worker','scope',1,1)`, tc.parent, tc.child); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO run VALUES('parent','pc','root',NULL,'worker','running','running','task','parent',1,'agent','conversation',0,'model')`); err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`INSERT INTO run VALUES('child','cc','child','parent','worker','queued','running','task','parent',1,'agent','conversation',1,'model')`)
			if (err == nil) != tc.allowed {
				t.Fatalf("child inheritance allowed=%v,want=%v,error=%v", err == nil, tc.allowed, err)
			}
		})
	}
}
