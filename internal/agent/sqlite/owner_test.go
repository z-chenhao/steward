package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMemoryRuntimeOwnerIsDurableAndCannotBeReassigned(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.BindOwner(ctx, "tenant-a:user-a"); e != nil {
		t.Fatal(e)
	}
	if s.OwnerID() != "tenant-a:user-a" {
		t.Fatal(s.OwnerID())
	}
	if e = s.BindOwner(ctx, "tenant-b:user-b"); e == nil {
		t.Fatal("owner reassigned")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.OwnerID() != "tenant-a:user-a" {
		t.Fatal("reopen lost identity", s.OwnerID())
	}
	if e = s.BindOwner(ctx, ""); e == nil {
		t.Fatal("implicit local owner replaced tenant")
	}
	if e = s.BindOwner(ctx, "tenant-a:user-a"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE runtime_owner SET user_id='attacker'`); e == nil {
		t.Fatal("mutable ownership")
	}
	// Existing local business data cannot be relabeled as another principal.
	old, _, _, _, _ := loopFixture(t)
	if e = old.BindOwner(ctx, "another-user"); e == nil {
		t.Fatal("legacy data reassigned", filepath.Base(old.path))
	}
}
