package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/memory"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector/contract"
	"github.com/z-chenhao/assistant/internal/recall"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func TestMemoryInspectionProjectionPrecedesOriginalSnapshotReplacementAndForgetChecks(t *testing.T) {
	for _, change := range []string{"replacement", "forget"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			s, r, _, _, _ := loopFixture(t)
			store, _ := evidenceMemoryVault(t, s)
			owner := idAgent(t, s)
			if e := store.Apply(ctx, evidenceFact("before-projection", owner)); e != nil {
				t.Fatal(e)
			}
			called := false
			page, e := r.inspectMemory(ctx, memory.InspectionQuery{}, store, func(ctx context.Context, _ evidenceAccess, p memory.InspectionPage) error {
				called = true
				if len(p.Items) != 1 {
					t.Fatal("projection did not receive original authorized page", p)
				}
				if change == "replacement" {
					next := evidenceFact("after-projection", owner)
					next.Supersedes = "before-projection"
					return store.Apply(ctx, next)
				}
				return store.Forget(ctx, recall.Scope{UserID: artifact.LocalUser, AgentID: owner}, "before-projection", time.Now())
			})
			if !called || !errors.Is(e, memory.ErrInspectionUnavailable) || len(page.Items) != 0 {
				t.Fatal("projection bypassed original snapshot recheck", called, page, e)
			}
		})
	}
}

func TestMemoryInspectionProjectionFailureDoesNotReleaseAnyReadBranch(t *testing.T) {
	for _, q := range []memory.InspectionQuery{{}, {Layer: memory.Transcripts}, {View: "maintenance"}} {
		t.Run(q.Layer+q.View, func(t *testing.T) {
			ctx := context.Background()
			s, r, _, _, _ := loopFixture(t)
			store, _ := evidenceMemoryVault(t, s)
			called := false
			page, e := r.inspectMemory(ctx, q, store, func(context.Context, evidenceAccess, memory.InspectionPage) error {
				called = true
				return memory.ErrInspectionUnavailable
			})
			if !called || !errors.Is(e, memory.ErrInspectionUnavailable) || len(page.Items) != 0 || len(page.Maintenance) != 0 {
				t.Fatal("failed projection released page", called, page, e)
			}
		})
	}
}

func TestMemoryInspectionPresentationRejectsForeignRoutesAndInventedIdentity(t *testing.T) {
	req := contract.Request{Navigation: []contract.Navigation{{Query: contract.Query{Ref: "memory:original", Source: "conversation:original"}, Key: "source", Value: "conversation:next"}}}
	labels := contract.Labels{Source: contract.SourceLabels{Conversation: "a", Task: "b", Tool: "c", Control: "d", Other: "e"}, Origin: contract.OriginLabels{Written: "a", Dream: "b", Original: "c", Automatic: "d", Explicit: "e", Other: "f"}, Layer: contract.LayerLabels{Transcripts: "a", Representations: "b", Injected: "c", Notes: "d"}, DefaultStatus: "active"}
	view := contract.View{Resource: contract.ViewResource, Source: "sealed private display source", Digest: rsiplugin.FileDigest([]byte("sealed private display source"))}
	styles := contract.Stylesheet{Resource: contract.StylesheetResource, Source: "sealed private styles", Digest: rsiplugin.FileDigest([]byte("sealed private styles"))}
	copy := contract.Localization{Resource: contract.LocalizationResource, Source: `[{"english":"Memory","chinese":"记忆"}]`}
	copy.Digest = rsiplugin.FileDigest([]byte(copy.Source))
	program := contract.BrowserProgram{Resource: contract.ProgramResource, Source: "self.onmessage=()=>{}"}
	program.Digest = rsiplugin.FileDigest([]byte(program.Source))
	digests := memoryInspectorDigests{View: view.Digest, Styles: styles.Digest, Copy: copy.Digest, Program: program.Digest}
	original := "/developer/memory?ref=memory%3Aoriginal&source=conversation%3Anext"
	if _, e := acceptMemoryInspectionPresentation("frozen", req, contract.Result{Program: program, Localization: copy, Stylesheet: styles, View: view, URLs: []string{original}, Labels: labels}, digests); e != nil {
		t.Fatal("original bound route rejected", e)
	}
	for _, bad := range []string{"/developer/memory", "https://outside.invalid/developer/memory", "//outside.invalid/developer/memory", "/developer/memory#private", "/other?ref=memory%3Aoriginal", "/developer/memory?sql=select", "/developer/memory?ref=memory%3Aforeign", "/developer/memory?source=conversation%3Aforeign", "/developer/memory?ref=memory%3Aoriginal&ref=memory%3Aoriginal", "/developer/%6demory"} {
		t.Run(bad, func(t *testing.T) {
			if _, e := acceptMemoryInspectionPresentation("frozen", req, contract.Result{Program: program, Localization: copy, Stylesheet: styles, View: view, URLs: []string{bad}, Labels: labels}, digests); !errors.Is(e, memory.ErrInspectionUnavailable) {
				t.Fatal("unsafe route accepted", bad, e)
			}
		})
	}
	if _, e := acceptMemoryInspectionPresentation("frozen", req, contract.Result{Program: program, Localization: copy, Stylesheet: styles, View: view, URLs: []string{}, Labels: labels}, digests); e == nil {
		t.Fatal("missing route accepted")
	}
	forged := view
	forged.Digest = rsiplugin.FileDigest([]byte("other source"))
	if _, e := acceptMemoryInspectionPresentation("frozen", req, contract.Result{Program: program, Localization: copy, Stylesheet: styles, View: forged, URLs: []string{original}, Labels: labels}, digests); e == nil {
		t.Fatal("foreign declared view digest accepted")
	}
	forged = view
	forged.Source = "other source"
	if _, e := acceptMemoryInspectionPresentation("frozen", req, contract.Result{Program: program, Localization: copy, Stylesheet: styles, View: forged, URLs: []string{original}, Labels: labels}, digests); e == nil {
		t.Fatal("unsealed source accepted")
	}
	for _, source := range []string{`[]`, `null`, `[{"english":"Memory","chinese":""}]`, `[{"english":"Memory","chinese":"记忆","effect":"send"}]`, `[{"english":"Memory","chinese":"记忆"},{"english":"Memory","chinese":"另一份"}]`, `[{"english":"Memory","chinese":"记忆"}] {}`} {
		badCopy := copy
		badCopy.Source = source
		badCopy.Digest = rsiplugin.FileDigest([]byte(source))
		expected := digests
		expected.Copy = badCopy.Digest
		if _, e := acceptMemoryInspectionPresentation("frozen", req, contract.Result{Program: program, Localization: badCopy, Stylesheet: styles, View: view, URLs: []string{original}, Labels: labels}, expected); e == nil {
			t.Fatal("malformed/ambiguous copy accepted", source)
		}
	}
	labels.Origin.Other = ""
	if _, e := acceptMemoryInspectionPresentation("frozen", req, contract.Result{Program: program, Localization: copy, Stylesheet: styles, View: view, URLs: []string{original}, Labels: labels}, digests); e == nil {
		t.Fatal("missing display classification accepted")
	}
}
