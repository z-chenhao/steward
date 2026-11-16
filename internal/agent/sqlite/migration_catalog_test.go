package sqlite

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// A file and fixture version can agree while a missing embed leaves real stores
// without the forward schema. Check the actual production migration catalog.
func TestMigrationCatalogContainsEveryForwardSource(t *testing.T) {
	files, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	sources := []string{}
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".sql") {
			sources = append(sources, f.Name())
		}
	}
	if len(sources) != len(migrations) {
		t.Fatalf("forward sources=%d actual migration catalog=%d", len(sources), len(migrations))
	}
	for i, name := range sources {
		if !strings.HasPrefix(name, fmt.Sprintf("%03d_", i+1)) {
			t.Fatal("noncontiguous forward migration", name)
		}
		raw, err := os.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != migrations[i] {
			t.Fatal("forward source missing/reordered in production catalog", name)
		}
	}
}
