package sqlite

import (
	"context"
	"testing"
)

func TestSemanticUpgradeAdoptsPopulatedPreRelease47(t *testing.T) {
	s, r, _, a, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, a, "preserve", "我喜欢无糖绿茶")
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if e := r.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	var request, result string
	if e := s.db.QueryRow(`SELECT request_sealed,result_sealed FROM memory_generation`).Scan(&request, &result); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(removeLayeredMemoryFixtureSchema + `ALTER TABLE memory_generation DROP COLUMN purpose; ALTER TABLE memory_generation DROP COLUMN pass_index; DROP TABLE generation_cost_estimate; PRAGMA user_version=47;`); e != nil {
		t.Fatal(e)
	}
	if e := migrate(ctx, s.db, migrations); e != nil {
		t.Fatal(e)
	}
	var nextRequest, nextResult string
	s.db.QueryRow(`SELECT request_sealed,result_sealed FROM memory_generation`).Scan(&nextRequest, &nextResult)
	if scalar(t, s, `PRAGMA user_version`) != 51 || request != nextRequest || result != nextResult || scalar(t, s, `SELECT count(*) FROM memory_semantic_receipt`) != 1 || scalar(t, s, `SELECT count(*) FROM provider_usage`) != 1 || scalar(t, s, `SELECT count(*) FROM generation_cost_estimate`) != 0 {
		t.Fatal("adoption changed private accepted work")
	}
	if e := r.CurateMemory(ctx); e != nil || p.calls != 1 {
		t.Fatal("adopted result generated twice", e, p.calls)
	}
}
func TestSemanticUpgradePublic47AndMalformedPrivate47(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(map[bool]string{false: "public", true: "malformed-private"}[malformed], func(t *testing.T) {
			s, _, _, _, _ := loopFixture(t)
			ctx := context.Background()
			if malformed {
				if _, e := s.db.Exec(removeLayeredMemoryFixtureSchema + `ALTER TABLE memory_generation DROP COLUMN purpose; ALTER TABLE memory_generation DROP COLUMN pass_index; DROP TABLE generation_cost_estimate; ALTER TABLE memory_generation ADD COLUMN unknown TEXT; PRAGMA user_version=47;`); e != nil {
					t.Fatal(e)
				}
			} else {
				if _, e := s.db.Exec(removeLayeredMemoryFixtureSchema + `DROP TABLE memory_cost_reservation; DROP TABLE memory_generation; DROP TABLE memory_semantic_receipt; PRAGMA user_version=47;`); e != nil {
					t.Fatal(e)
				}
			}
			err := migrate(ctx, s.db, migrations)
			if malformed {
				if err == nil || scalar(t, s, `PRAGMA user_version`) != 47 || scalar(t, s, `SELECT count(*) FROM sqlite_schema WHERE name='generation_cost_estimate'`) != 0 {
					t.Fatal("unknown schema accepted or partly migrated", err)
				}
			} else {
				if err != nil || scalar(t, s, `PRAGMA user_version`) != 51 || scalar(t, s, `SELECT count(*) FROM memory_generation`) != 0 {
					t.Fatal(err)
				}
			}
		})
	}
}
