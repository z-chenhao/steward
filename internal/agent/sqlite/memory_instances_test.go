package sqlite

import (
	"context"
	"github.com/z-chenhao/assistant/internal/llm"
	"path/filepath"
	"testing"

	memorysqlite "github.com/z-chenhao/assistant/internal/memory/sqlite"
)

func TestMemoryTwoHostsShareLeaseWithoutDuplicateInferenceOrReceipts(t *testing.T) {
	s, r, _, actor, p := semanticRuntime(t)
	ctx := context.Background()
	mustSubmit(t, actor, "shared-observation", "我长期偏好无糖绿茶，请记住")
	if e := r.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	other, e := Open(ctx, filepath.Dir(s.path))
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	m, e := memorysqlite.Open(ctx, filepath.Dir(s.path))
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if e = m.ConfigureVault(r.vault); e != nil {
		t.Fatal(e)
	}
	m.ConfigureLocal()
	r2 := other.Runs(r.history, r.registry)
	r2.ConfigureMemory(m, r.vault)
	p2 := &semanticProvider{r: r2, reply: p.reply}
	r2.ConfigureSemanticMemory(p2)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	p.beforeReply = func(context.Context, llm.GenerationRequest) { close(entered); <-release }
	go func() { done <- r.CurateMemory(ctx) }()
	<-entered
	e = r2.CurateMemory(ctx)
	close(release)
	if first := <-done; first != nil {
		t.Fatal(first)
	}
	if e != nil {
		t.Fatal(e)
	}
	if e = r2.CurateMemory(ctx); e != nil {
		t.Fatal(e)
	}
	if p.calls != 1 || p2.calls != 0 || scalar(t, s, `SELECT count(*) FROM memory_generation`) != 1 || scalar(t, s, `SELECT count(*) FROM memory_semantic_receipt`) != 1 {
		t.Fatal("duplicate maintenance", p.calls, p2.calls)
	}
	if scalar(t, s, `SELECT count(*) FROM memory_mutation WHERE last_error IS NOT NULL`) != 0 {
		t.Fatal("lease contention recorded as failure")
	}
}
