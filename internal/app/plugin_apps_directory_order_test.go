package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/connector/composio"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsdirectory/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestAppsDirectoryActualOrderCompleteSourceVersionColdAndInvalidWindows(t *testing.T) {
	ctx := context.Background()
	raw, err := os.ReadFile("../../tests/fixtures/plugins/apps-catalogue-order-large-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var large struct {
		Count          int
		ExpectedSHA256 string
	}
	if err := json.Unmarshal(raw, &large); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../tests/fixtures/plugins/apps-catalogue-normalized-input-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var normalized []struct {
		Name                                                string
		SourceBodyBytes, DecodedNameBytes, DecodedNameRunes int
		NameSHA256                                          string
		Expected                                            []int
	}
	if err := json.Unmarshal(raw, &normalized); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	secrets := memory.New()
	// Original request accepts a nonempty one-byte key; its existing redaction
	// expands1MiB to10MiB after the body cap. This is an IO fixture, not account creation.
	if err := secrets.Put(ctx, "credential:order-source", []byte("q")); err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	var requests atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/toolkits" || r.Header.Get("x-api-key") != "q" || q.Get("type") != "native" || q.Get("include_deprecated") != "false" || q.Get("sort_by") != "alphabetically" {
			t.Error("original source authorization changed")
			http.Error(w, "invalid", 400)
			return
		}
		mode := q.Get("search")
		if mode == "redaction-expansion" || mode == "invalid-utf8-expansion" {
			name := strings.Repeat("q", 1<<20)
			if mode == "invalid-utf8-expansion" {
				name = strings.Repeat(string([]byte{255}), 3<<20)
			}
			_, _ = w.Write([]byte(`{"items":[{"slug":"one","name":"` + name + `"},{"slug":"two","name":"A"}]}`))
			return
		}
		count := large.Count
		switch mode {
		case "bad-duplicate":
			count = 7
		case "bad-bounds":
			count = 8
		case "bad-short":
			count = 9
		case "bad-second-window":
			count = contract.OrderPageSize + 3
		}
		type row struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		}
		rows := make([]row, count)
		for i := range rows {
			n := count - 1 - i
			rows[i] = row{Slug: fmt.Sprintf("row_%06d", n), Name: fmt.Sprintf("Row %06d", n)}
		}
		_ = json.NewEncoder(w).Encode(struct {
			Items []row `json:"items"`
		}{rows})
	}))
	defer peer.Close()
	var host rsi.Host
	currentGraph := ""
	offsets := []int{}
	bind := func() {
		host = a.Plugins()
		host.Catalog.Launcher = carrier
		a.connectedApps.Client = composio.Client{Endpoint: peer.URL}
		a.connectedApps.ReadEditorial = func(c context.Context, query contract.Query) (contract.Catalogue, bool, *connectedapps.DirectoryMethods, error) {
			value, managed, methods, err := a.readAppsEditorialWithHost(c, host, query)
			if methods != nil {
				realOrder := methods.Order
				revision := currentGraph
				methods.Order = func(c context.Context, request contract.OrderRequest, data []byte) (contract.OrderPage, error) {
					offsets = append(offsets, request.Offset)
					page, err := realOrder(c, request, data)
					if err == nil && query.Search == "late-order" && request.Offset == 0 {
						if err := host.Store.Quarantine(c, revision); err != nil {
							t.Fatal(err)
						}
					}
					return page, err
				}
			}
			return value, managed, methods, err
		}
	}
	bind()
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.KeyRef = "credential:order-source"
	if err := a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	first, _ := activateEditorialVersion(t, ctx, host, "", "1.0.0")
	currentGraph = first
	read := func(mode string) (connectedapps.DirectorySelection, connectedapps.View) {
		t.Helper()
		query := contract.Query{Search: mode, LiveConfigured: true}
		selection, err := a.connectedApps.Directory(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		before := requests.Load()
		view, err := a.connectedApps.View(connectedapps.WithEditorial(ctx, query, selection, nil), mode, "", "")
		if err != nil || view.Unavailable || requests.Load() != before+1 {
			t.Fatal("actual source changed/retried", mode, err, requests.Load()-before)
		}
		return selection, view
	}
	hash := func(indices []int) string {
		h := sha256.New()
		for _, index := range indices {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], uint32(index))
			_, _ = h.Write(b[:])
		}
		return fmt.Sprintf("%x", h.Sum(nil))
	}
	checkLarge := func(beta bool) (connectedapps.DirectorySelection, connectedapps.View) {
		t.Helper()
		selection, view := read("large-order")
		if len(view.Catalog.Items) != large.Count {
			t.Fatal("original source rows capped", len(view.Catalog.Items))
		}
		offsets = nil
		before := requests.Load()
		indices, err := selection.Order(ctx, view.Catalog.Items)
		if err != nil || len(indices) != large.Count || requests.Load() != before || !reflect.DeepEqual(offsets, []int{0, 16384, 32768, 49152}) {
			t.Fatal("complete output windows failed", len(indices), offsets, err)
		}
		if beta {
			for i, index := range indices {
				if index != i {
					t.Fatal("independent descending algorithm ignored", i, index)
				}
			}
		} else if hash(indices) != large.ExpectedSHA256 {
			t.Fatal("captured full native order changed", hash(indices))
		}
		return selection, view
	}
	frozen, original := checkLarge(false)
	for _, c := range normalized {
		selection, view := read(c.Name)
		if len(view.Catalog.Items) != 2 {
			t.Fatal("normalized source incomplete", c.Name, len(view.Catalog.Items))
		}
		name := view.Catalog.Items[0].Name
		if len(name) != c.DecodedNameBytes || utf8.RuneCountInString(name) != c.DecodedNameRunes || fmt.Sprintf("%x", sha256.Sum256([]byte(name))) != c.NameSHA256 {
			t.Fatal("original native normalization altered", c.Name, len(name))
		}
		indices, err := selection.Order(ctx, view.Catalog.Items)
		if err != nil || !reflect.DeepEqual(indices, c.Expected) {
			t.Fatal("expanded native facts failed new input/order path", c.Name, indices, c.Expected, err)
		}
	}
	second, _ := activateEditorialVersion(t, ctx, host, first, "1.1.0")
	currentGraph = second
	checkLarge(true)
	indices, err := frozen.Order(ctx, original.Catalog.Items)
	if err != nil || hash(indices) != large.ExpectedSHA256 {
		t.Fatal("frozen request followed active order algorithm", err)
	}
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
	}
	reopen()
	checkLarge(true)
	if err := host.Propose(ctx, rsi.Candidate{ID: "order-rollback", Baseline: second, Target: first, PolicyID: "editorial-owner-1.0.0", Goal: "Restore captured original catalogue ordering"}); err != nil {
		t.Fatal(err)
	}
	decision, err := host.Verify(ctx, "order-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Activate(ctx, rsi.Activation{OperationID: "order-rollback", Expected: second, Target: first, DecisionID: decision}); err != nil {
		t.Fatal(err)
	}
	currentGraph = first
	reopen()
	checkLarge(false)
	third, _ := activateEditorialVersion(t, ctx, host, first, "2.0.0")
	currentGraph = third
	for _, mode := range []string{"bad-duplicate", "bad-bounds", "bad-short", "bad-second-window"} {
		selection, view := read(mode)
		before := requests.Load()
		indices, err := selection.Order(ctx, view.Catalog.Items)
		if !errors.Is(err, connectedapps.ErrUnavailable) || indices != nil || requests.Load() != before {
			t.Fatal("invalid output released partial/native order or retried source", mode, len(indices), err)
		}
	}
	late, view := read("late-order")
	offsets = nil
	before := requests.Load()
	if indices, err := late.Order(ctx, view.Catalog.Items); !errors.Is(err, connectedapps.ErrUnavailable) || indices != nil || !reflect.DeepEqual(offsets, []int{0, 16384}) || requests.Load() != before {
		t.Fatal("late quarantine released first window or retried", len(indices), offsets, err)
	}
	if _, err := host.Store.Deactivate(ctx, "order-disable", third); err != nil {
		t.Fatal(err)
	}
	reopen()
	before = requests.Load()
	if _, err := a.connectedApps.View(ctx, "large-order", "", ""); !errors.Is(err, connectedapps.ErrUnavailable) || requests.Load() != before {
		t.Fatal("cold deactivation restored native catalogue", err)
	}
}
