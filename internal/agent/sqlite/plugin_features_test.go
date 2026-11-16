package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func TestV52FeatureSnapshotMigrationPreservesOldPoliciesAndRequests(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err = migrate(ctx, db, migrations[:52]); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"legacy", "managed"} {
		_, err = db.Exec(`INSERT INTO run(run_id,agent_id,conversation_id,kind,status,capability_snapshot_id,model_snapshot_id,checkpoint_json,history_boundary,created_at,updated_at,version) VALUES(?,?,?,'main','queued','cap','model','{}',0,1,1,1)`, id, id, id)
		if err != nil {
			t.Fatal(err)
		}
		policy := `{"threshold_percentage":0.75,"recent_messages":6,"summary_bytes":2048}`
		if id == "managed" {
			policy = `{"docx_plugin":true,"image_ocr_plugin":true,"threshold_percentage":0.75,"recent_messages":6,"summary_bytes":2048}`
		}
		if _, err = db.Exec(`INSERT INTO run_context_policy VALUES(?,?)`, id, policy); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO rsi_run VALUES(?,NULL)`, id); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO context_compaction VALUES(?,0,'policy-hash','input-hash','[{"role":"system","text":"frozen"}]','{}',1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err = db.QueryRow(`SELECT group_concat(policy_json,'|') FROM run_context_policy ORDER BY run_id`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"legacy": `[]`, "managed": `["steward.docx-text/1","steward.image-ocr/1"]`} {
		var got, request string
		if err = db.QueryRow(`SELECT managed_services_json FROM rsi_run WHERE run_id=?`, id).Scan(&got); err != nil || got != want {
			t.Fatal(id, got, err)
		}
		if err = db.QueryRow(`SELECT messages_json FROM context_compaction WHERE run_id=?`, id).Scan(&request); err != nil || request != `[{"role":"system","text":"frozen"}]` {
			t.Fatal("frozen request changed", request, err)
		}
	}
	var after string
	if err = db.QueryRow(`SELECT group_concat(policy_json,'|') FROM run_context_policy ORDER BY run_id`).Scan(&after); err != nil || after != before {
		t.Fatal("policies rewritten", err)
	}
	if _, err = db.Exec(`UPDATE rsi_run SET managed_services_json='[]' WHERE run_id='managed'`); err == nil {
		t.Fatal("mutable migration boundary")
	}
}
