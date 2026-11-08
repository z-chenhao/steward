package sqlite

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestGenerationDetailsUseOnlySelectedFrozenCallAndKeepUnknownUsage(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE model_generation(generation_id TEXT,request_json TEXT);
 CREATE TABLE provider_usage(generation_id TEXT,input_tokens INTEGER,output_tokens INTEGER,cache_read_tokens INTEGER,cache_write_tokens INTEGER,latency_ms INTEGER);
 CREATE TABLE spend_reservation(generation_id TEXT,actual_micros INTEGER,currency TEXT);
 CREATE TABLE generation_cost_estimate(generation_id TEXT,estimated_micros INTEGER,currency TEXT,source TEXT,price_json TEXT);
 INSERT INTO model_generation VALUES('one','{"model_snapshot":{"model_id":"frozen-model","provider_kind":"openai_compatible","wire_api":"openai_completions","reasoning_effort":"high","max_output_tokens":8000,"generation_defaults":{"temperature":0}}}'),('missing','{}');
 INSERT INTO provider_usage VALUES('one',10,0,20,30,123),('other',999,999,999,999,999);
 INSERT INTO spend_reservation VALUES('one',0,'USD');
 INSERT INTO generation_cost_estimate VALUES('one',42,'USD','reference','{"version":"v1"}');`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	d, err := generationDetails(context.Background(), tx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if d.Model != "frozen-model" || d.Provider != "openai_compatible" || d.ReasoningEffort != "high" || d.Parameters != `{"temperature":0}` || d.MaxOutputTokens != 8000 {
		t.Fatal(d)
	}
	m := d.Metrics
	if m.KnownUsage != 1 || m.Input != 10 || m.Output != 0 || m.CacheRead != 20 || m.CacheWrite != 30 || m.ProviderMS == nil || *m.ProviderMS != 123 {
		t.Fatal(m)
	}
	if len(m.Costs) != 1 || m.Costs[0].Micros != 0 || m.Costs[0].Estimated {
		t.Fatal("recorded zero charge lost to estimate", m)
	}
	missing, err := generationDetails(context.Background(), tx, "missing")
	if err != nil || missing.Metrics.KnownUsage != 0 || missing.Metrics.ProviderMS != nil || len(missing.Metrics.Costs) != 0 {
		t.Fatal(missing, err)
	}
	if _, err = generationDetails(context.Background(), tx, "absent"); err != sql.ErrNoRows {
		t.Fatal("missing call became zero metrics", err)
	}
}
