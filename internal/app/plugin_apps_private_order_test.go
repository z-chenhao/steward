package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	rsistore "github.com/z-chenhao/assistant/internal/rsi/sqlite"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	xhtml "golang.org/x/net/html"
)

func TestAppsDirectoryActualPrivateOrderLargeOriginalColdAndFences(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-private-account-order-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name                                                            string
		Count, PadRepeats, GiantIndex, GiantRepeats, ProjectedNameBytes int
		ProjectedNamesSHA256, SidebarOrderSHA256, ConnectedOrderSHA256  string
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	const token = "synthetic-private-order-owner-token-24-characters"
	if err := secrets.Put(ctx, "credential:private-order-owner", []byte(token)); err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	if _, err := a.BindChannel(ctx, "web", "browser", "local", true, 0); err != nil {
		t.Fatal(err)
	}
	carrier := filepath.Join(t.TempDir(), "steward")
	if out, err := exec.Command("go", "build", "-o", carrier, "../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	var host rsi.Host
	var offsets []int
	maxBytes := 0
	mutateAfterWindow := false
	quarantineAfterWindow := false
	currentGraph := ""
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.ReadEditorial = func(c context.Context, q contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
			value, managed, methods, err := a.readAppsEditorialWithHost(c, host, q)
			if methods != nil {
				original := methods.InventoryOrder
				graph := currentGraph
				methods.InventoryOrder = func(c context.Context, revision int64, request contract.InventoryOrderRequest, data []byte) (contract.OrderPage, error) {
					offsets = append(offsets, request.Offset)
					maxBytes = max(maxBytes, len(data))
					if strings.Contains(string(data), "private account id") || strings.Contains(string(data), "private alias") {
						t.Fatal("unneeded account identity disclosed")
					}
					page, err := original(c, revision, request, data)
					if err == nil && request.Offset == 0 && mutateAfterWindow {
						mutateAfterWindow = false
						state, e := a.store.LoadApps(c)
						if e != nil {
							t.Fatal(e)
						}
						if e := a.store.SaveApps(c, state, state.Revision); e != nil {
							t.Fatal(e)
						}
					}
					if err == nil && request.Offset == 0 && quarantineAfterWindow {
						quarantineAfterWindow = false
						if e := host.Store.Quarantine(c, graph); e != nil {
							t.Fatal(e)
						}
					}
					return page, err
				}
			}
			return value, managed, methods, err
		}
	}
	bind()
	save := func(rows []connectedapps.Connection) connectedapps.State {
		t.Helper()
		state, err := a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.Connections = rows
		if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	selection := func() connectedapps.DirectorySelection {
		t.Helper()
		s, err := a.connectedApps.Directory(ctx, contract.Query{})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	factsFor := func(state connectedapps.State, s connectedapps.DirectorySelection) []contract.InventoryOrderFact {
		t.Helper()
		facts := make([]contract.InventoryOrderFact, len(state.Connections))
		for i, row := range state.Connections {
			label, err := s.Label(row.Name, row.Toolkit)
			if err != nil {
				t.Fatal(err)
			}
			facts[i] = contract.InventoryOrderFact{Name: label.Name, Slug: row.Toolkit}
		}
		return facts
	}
	hashOrder := func(facts []contract.InventoryOrderFact, indices []int) string {
		h := sha256.New()
		for _, i := range indices {
			_, _ = h.Write([]byte(facts[i].Slug + "\n"))
		}
		return hex.EncodeToString(h.Sum(nil))
	}
	first, _ := activateEditorialVersion(t, ctx, host, "", "1.0.0")
	currentGraph = first
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			rows := make([]connectedapps.Connection, fixture.Count)
			for i := range rows {
				prefix := fmt.Sprintf("%06d", (i*7919)%10003)
				switch i % 19 {
				case 0:
					prefix = ""
				case 1:
					prefix = "a"
				case 2:
					prefix = "A"
				case 3:
					prefix = "é"
				case 4:
					prefix = "e\u0301"
				case 5:
					prefix = " Ω"
				case 6:
					prefix = "same"
				case 7:
					prefix = "SAME"
				}
				name := prefix + strings.Repeat("x", fixture.PadRepeats)
				if i == fixture.GiantIndex {
					name = "巨大" + strings.Repeat("Z", fixture.GiantRepeats)
				}
				rows[i] = connectedapps.Connection{Toolkit: fmt.Sprintf("app%05d", i), Name: name, ID: "private account id", Alias: "private alias", Status: "active"}
			}
			state := save(rows)
			s := selection()
			facts := factsFor(state, s)
			h := sha256.New()
			size := 0
			for _, fact := range facts {
				var n [8]byte
				binary.LittleEndian.PutUint64(n[:], uint64(len(fact.Name)))
				_, _ = h.Write(n[:])
				_, _ = h.Write([]byte(fact.Name))
				size += len(fact.Name)
			}
			if size != fixture.ProjectedNameBytes || hex.EncodeToString(h.Sum(nil)) != fixture.ProjectedNamesSHA256 {
				t.Fatal("native input generation changed")
			}
			for _, connected := range []bool{false, true} {
				expected := fixture.SidebarOrderSHA256
				if connected {
					expected = fixture.ConnectedOrderSHA256
				}
				indices, err := s.InventoryOrder(ctx, state.Revision, facts, connected)
				if err != nil || hashOrder(facts, indices) != expected {
					t.Fatal("captured native private order changed", connected, len(indices), err)
				}
			}
		})
	}
	if maxBytes <= rsiplugin.MaxInputBytes {
		t.Fatal("large source did not cross ordinary cap", maxBytes)
	}
	// Revocation during a multi-chunk stream must abort before releasing a result.
	streamState := save([]connectedapps.Connection{{Toolkit: "stream-example", Name: strings.Repeat("private streamed display", 500000), Status: "active"}})
	streamBase := *host.Store.(*rsistore.Store)
	armed := false
	checks := 0
	streamBase.Fence = func(context.Context, *sql.Tx) error {
		if armed {
			checks++
			if checks >= 8 {
				return rsi.ErrRevoked
			}
		}
		return nil
	}
	streamHost := host
	streamHost.Store = &streamBase
	_, _, streamMethods, err := a.readAppsEditorialWithHost(ctx, streamHost, contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	streamFacts := factsFor(streamState, selection())
	armed = true
	if page, err := streamMethods.InventoryOrder(ctx, streamState.Revision, contract.InventoryOrderRequest{Count: len(streamFacts)}, contract.EncodeInventoryOrderFacts(streamFacts)); !errors.Is(err, rsi.ErrRevoked) || page.Indices != nil || checks < 8 {
		t.Fatal("mid-stream original fence was ignored", checks, err)
	}
	// The ordinary entrypoint still rejects the same authorized source.
	state := save([]connectedapps.Connection{{Toolkit: "example", Status: "active"}})
	input, _ := json.Marshal(contract.InventoryOrderRequest{Count: 1})
	if out, err := host.ReadService(ctx, first, contract.InventoryOrder, input, []rsi.InputData{{ID: contract.InventoryOrderInputID, Data: make([]byte, rsiplugin.MaxInputBytes+1)}}); !errors.Is(err, rsi.ErrInvalid) || out != nil {
		t.Fatal("ordinary pure read cap widened", err)
	}
	// Stream reads retain the existing independent Host Fence.
	base := *host.Store.(*rsistore.Store)
	denied := false
	base.Fence = func(context.Context, *sql.Tx) error {
		if denied {
			return rsi.ErrRevoked
		}
		return nil
	}
	guarded := host
	guarded.Store = &base
	_, _, methods, err := a.readAppsEditorialWithHost(ctx, guarded, contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	denied = true
	if _, err := methods.InventoryOrder(ctx, state.Revision, contract.InventoryOrderRequest{Count: 1}, contract.EncodeInventoryOrderFacts(factsFor(state, selection()))); !errors.Is(err, rsi.ErrRevoked) {
		t.Fatal("stream replaced original Fence", err)
	}
	// Stale private facts fail before starting a process.
	_, _, methods, err = a.readAppsEditorialWithHost(ctx, host, contract.Query{})
	if err != nil {
		t.Fatal(err)
	}
	save(nil)
	offline := carrier + ".offline"
	if err := os.Rename(carrier, offline); err != nil {
		t.Fatal(err)
	}
	_, stale := methods.InventoryOrder(ctx, state.Revision, contract.InventoryOrderRequest{Count: 1}, contract.EncodeInventoryOrderFacts([]contract.InventoryOrderFact{{Name: "private"}}))
	if err := os.Rename(offline, carrier); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(stale, connectedapps.ErrConflict) {
		t.Fatal("stale private order needed executable or escaped fence", stale)
	}
	small := []connectedapps.Connection{{Toolkit: "one", Name: "z", Status: "active"}, {Toolkit: "two", Name: "A", Status: "active"}, {Toolkit: "three", Name: "b", Status: "active"}}
	state = save(small)
	frozen := selection()
	facts := factsFor(state, frozen)
	server := &web.Server{Config: web.Config{TokenRef: "credential:private-order-owner"}, Core: a, Secrets: secrets, Apps: a.connectedApps}
	get := func() (int, string) {
		request := httptest.NewRequest("GET", "http://127.0.0.1:8080/settings/apps?view=connected", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	assertHTML := func(want []string) {
		t.Helper()
		code, body := get()
		if code != 200 {
			t.Fatal("actual order page unavailable", code)
		}
		doc, err := xhtml.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		var walk func(*xhtml.Node)
		walk = func(n *xhtml.Node) {
			if n.Type == xhtml.ElementNode && n.Data == "article" {
				for _, a := range n.Attr {
					if a.Key == "data-app-tile" {
						got = append(got, a.Val)
					}
				}
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		walk(doc)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("actual connected HTML ignored order", got, want)
		}
	}
	assertHTML([]string{"two", "three", "one"})
	second, _ := activateEditorialVersion(t, ctx, host, first, "1.3.0")
	currentGraph = second
	if !reflect.DeepEqual(frozen.Catalogue, selection().Catalogue) {
		t.Fatal("private order version changed unrelated metadata")
	}
	if indices, err := selection().InventoryOrder(ctx, state.Revision, facts, true); err != nil || !reflect.DeepEqual(indices, []int{0, 2, 1}) {
		t.Fatal("independent comparator ignored", indices, err)
	}
	if indices, err := frozen.InventoryOrder(ctx, state.Revision, facts, true); err != nil || !reflect.DeepEqual(indices, []int{1, 2, 0}) {
		t.Fatal("frozen order followed active version", indices, err)
	}
	assertHTML([]string{"one", "three", "two"})
	reopen := func() {
		t.Helper()
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
		if err != nil {
			t.Fatal(err)
		}
		a.eventsRequired.Store(false)
		bind()
		server.Core, server.Apps = a, a.connectedApps
	}
	reopen()
	assertHTML([]string{"one", "three", "two"})
	if err := host.Propose(ctx, rsi.Candidate{ID: "private-order-rollback", Baseline: second, Target: first, PolicyID: "editorial-owner-1.0.0", Goal: "Restore original private account order"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "private-order-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "private-order-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	currentGraph = first
	reopen()
	assertHTML([]string{"two", "three", "one"})
	// Output windows use the complete original source, not per-window sorting.
	rows := make([]connectedapps.Connection, contract.OrderPageSize+3)
	for i := range rows {
		rows[i] = connectedapps.Connection{Toolkit: fmt.Sprintf("row%06d", i), Name: fmt.Sprintf("Name%06d", len(rows)-i), Status: "active"}
	}
	state = save(rows)
	s := selection()
	facts = factsFor(state, s)
	offsets = nil
	indices, err := s.InventoryOrder(ctx, state.Revision, facts, true)
	if err != nil || len(indices) != len(rows) || indices[0] != len(rows)-1 || indices[len(indices)-1] != 0 || !reflect.DeepEqual(offsets, []int{0, 16384}) {
		t.Fatal("full private permutation/window changed", len(indices), offsets, err)
	}
	state = save(rows)
	s = selection()
	facts = factsFor(state, s)
	offsets = nil
	mutateAfterWindow = true
	if indices, err := s.InventoryOrder(ctx, state.Revision, facts, false); !errors.Is(err, connectedapps.ErrUnavailable) || indices != nil || !reflect.DeepEqual(offsets, []int{0, 16384}) {
		t.Fatal("late state change released partial order", offsets, err)
	}
	third, _ := activateEditorialVersion(t, ctx, host, first, "2.0.0")
	currentGraph = third
	for _, count := range []int{7, 8, 9, contract.OrderPageSize + 3} {
		state = save(rows[:count])
		s = selection()
		facts = factsFor(state, s)
		if indices, err := s.InventoryOrder(ctx, state.Revision, facts, true); !errors.Is(err, connectedapps.ErrUnavailable) || indices != nil {
			t.Fatal("invalid permutation or partial prefix escaped", count, len(indices), err)
		}
		if count == 7 {
			code, body := get()
			if code == 200 || strings.Contains(body, "Name016387") {
				t.Fatal("invalid order released private page", code)
			}
		}
	}
	state = save(rows)
	s = selection()
	facts = factsFor(state, s)
	offsets = nil
	quarantineAfterWindow = true
	if indices, err := s.InventoryOrder(ctx, state.Revision, facts, true); !errors.Is(err, connectedapps.ErrUnavailable) || indices != nil || !reflect.DeepEqual(offsets, []int{0, 16384}) {
		t.Fatal("late quarantine released partial order", offsets, err)
	}
	if _, err := host.Store.Deactivate(ctx, "private-order-disable", third); err != nil {
		t.Fatal(err)
	}
	reopen()
	if _, err := a.connectedApps.Directory(ctx, contract.Query{}); !errors.Is(err, connectedapps.ErrUnavailable) {
		t.Fatal("cold disable restored managed/native metadata", err)
	}
}
