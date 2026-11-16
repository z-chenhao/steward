package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/tool"
)

func TestMCPNamespaceSourceKeepsOriginalInstructionsAndExplicitOwnerChoice(t *testing.T) {
	c := config(t)
	serverText := "  " + strings.Repeat("原", 700) + "\nRAW_SERVER_DETAIL_NOT_A_PUBLIC_SUMMARY"
	initialize, _ := json.Marshal(map[string]any{"protocolVersion": protocol, "capabilities": map[string]any{"tools": map[string]any{}}, "instructions": serverText})
	if err := os.WriteFile(filepath.Join(c.Directory, "mcp-initialize.json"), initialize, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := Definitions(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range first {
		if d.NamespaceSource == nil || *d.NamespaceSource != serverText || d.Spec.NamespaceDescription != strings.Repeat("原", 512)+"…" {
			t.Fatal("raw source was pre-truncated or original view changed")
		}
	}
	c.Description = "  Exact owner summary\nRAW_OWNER_DETAIL_NOT_A_PUBLIC_SUMMARY"
	second, err := Definitions(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := tool.NewRegistry(second)
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range second {
		if d.NamespaceSource == nil || *d.NamespaceSource != c.Description || d.Spec.NamespaceDescription != "Exact owner summary" || d.Spec.ResourceHash != first[i].Spec.ResourceHash {
			t.Fatal("presentation source overrode explicit owner choice or rebound execution")
		}
	}
	raw, _ := json.Marshal(registry.Describe())
	if strings.Contains(string(raw), "RAW_SERVER_DETAIL") || strings.Contains(string(raw), "RAW_OWNER_DETAIL") {
		t.Fatal("unformatted namespace source entered a serialized capability")
	}
}
