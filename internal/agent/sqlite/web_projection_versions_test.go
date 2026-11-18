package sqlite

import (
	"context"
	"database/sql"
	"testing"
)

func TestStaticWebProjectionVersionsRejectRichAndAmbiguousRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE ui_widget(message_id TEXT);CREATE TABLE delivery_request(message_id TEXT,delivery_id TEXT PRIMARY KEY);CREATE TABLE delivery_projection(delivery_id TEXT,spec_json TEXT);
	INSERT INTO delivery_request VALUES('text','d1'),('rich','d2'),('many','d3'),('many','d4');
	INSERT INTO delivery_projection VALUES('d1','{"components":[{"type":"markdown"}]}'),('d2','{"components":[{"type":"form"}]}');
	INSERT INTO ui_widget VALUES('widget')`); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	ctx := context.Background()
	v, ok, err := store.StaticWebProjectionVersions(ctx, []string{"new", "text"})
	if err != nil || !ok || len(v) != 2 || v[0] != "" || v[1] != "d1" {
		t.Fatal(v, ok, err)
	}
	for _, id := range []string{"widget", "rich", "many"} {
		if _, ok, err = store.StaticWebProjectionVersions(ctx, []string{id}); err != nil || ok {
			t.Fatal(id, ok, err)
		}
	}
}
