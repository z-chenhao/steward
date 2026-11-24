package sqlite

import (
	"testing"
	"time"
)

func TestForgetScrubsRecallDocumentContentAndFTS(t *testing.T) {
	s := openTest(t)
	put(t, s, record("secret-1", "unique-forget-token-alpha"))

	if err := s.Forget(ctx, scope, "secret-1", time.Unix(200, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	var active int
	var content string
	var vecNull bool
	if err := s.db.QueryRowContext(ctx, `SELECT active, content, vector_json IS NULL FROM recall_document WHERE ref='memory:secret-1'`).Scan(&active, &content, &vecNull); err != nil {
		t.Fatal(err)
	}
	if active != 0 || content != "" || !vecNull {
		t.Fatalf("forgotten recall_document not scrubbed: active=%d content=%q vecNull=%v", active, content, vecNull)
	}
	var ftsHits int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM recall_fts WHERE recall_fts MATCH 'unique'`).Scan(&ftsHits); err != nil {
		t.Fatal(err)
	}
	if ftsHits != 0 {
		t.Fatalf("forgotten plaintext still present in recall_fts (%d hits)", ftsHits)
	}
}
