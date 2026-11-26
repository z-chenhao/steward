package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAppsAccountInventoryActualLargeWindowsAndInterWindowFence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(k string) (string, bool) { return root, k == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	a, err := OpenRuntimeWithSecrets(ctx, cfg, memory.New())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.eventsRequired.Store(false)
	carrier := filepath.Join(t.TempDir(), "steward")
	buildTestBinary(t, "../../cmd/steward", carrier)
	host := a.Plugins()
	host.Catalog.Launcher = carrier
	graph, policy := prepareAccountInventoryVersion(t, ctx, host, "1.8.0")
	activateInterface(t, ctx, host, "", graph, policy, "inventory-large")
	state, err := a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const count = contract.InventoryPageSize*2 + 3
	for i := 0; i < count; i++ {
		state.Connections = append(state.Connections, connectedapps.Connection{Toolkit: fmt.Sprintf("app_%04d", i), Name: fmt.Sprintf("%04d-", i) + strings.Repeat("private显示", 32), Status: "active", ID: "private-original-id", AccountID: "private-provider-id", Alias: "private-account-alias"})
	}
	if err = a.store.SaveApps(ctx, state, state.Revision); err != nil {
		t.Fatal(err)
	}
	state, err = a.store.LoadApps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.appsAccountInventoryWithHost(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := p.Project(ctx, state, true)
	if err != nil || len(projection.Rows) != count {
		t.Fatal("actual full inventory truncated", len(projection.Rows), err)
	}
	for i, n := range projection.Rows {
		if i != n {
			t.Fatal("dropped/replayed original row", i, n)
		}
	}

	// Retain the independently captured original >12MiB private-name case,
	// including its single 3MiB display name. It predates this inventory producer.
	raw, e := os.ReadFile("../../tests/fixtures/plugins/apps-private-account-order-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var original []struct {
		Name                                                            string
		Count, PadRepeats, GiantIndex, GiantRepeats, ProjectedNameBytes int
		ProjectedNamesSHA256, SidebarOrderSHA256, ConnectedOrderSHA256  string
	}
	if e = json.Unmarshal(raw, &original); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, fixture := range original {
		if fixture.Name != "large-private-names" {
			continue
		}
		found = true
		rows := make([]connectedapps.Connection, fixture.Count)
		h := sha256.New()
		size := 0
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
			rows[i] = connectedapps.Connection{Toolkit: fmt.Sprintf("app%05d", i), Name: name, Status: "active", ID: "private-original-id", AccountID: "private-provider-id", Alias: "private-account-alias"}
			var length [8]byte
			binary.LittleEndian.PutUint64(length[:], uint64(len(name)))
			_, _ = h.Write(length[:])
			_, _ = h.Write([]byte(name))
			size += len(name)
		}
		if size != fixture.ProjectedNameBytes || hex.EncodeToString(h.Sum(nil)) != fixture.ProjectedNamesSHA256 {
			t.Fatal("original large display input changed")
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state.Connections = rows
		if err = a.store.SaveApps(ctx, state, state.Revision); err != nil {
			t.Fatal(err)
		}
		state, err = a.store.LoadApps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, connected := range []bool{false, true} {
			projection, err = p.Project(ctx, state, connected)
			if err != nil || len(projection.Rows) != fixture.Count {
				t.Fatal("original large inventory truncated", len(projection.Rows), err)
			}
			h := sha256.New()
			for _, n := range projection.Rows {
				_, _ = h.Write([]byte(rows[n].Toolkit + "\n"))
			}
			want := fixture.SidebarOrderSHA256
			if connected {
				want = fixture.ConnectedOrderSHA256
			}
			if hex.EncodeToString(h.Sum(nil)) != want {
				t.Fatal("changed independently captured original large order", connected)
			}
		}
	}
	if !found {
		t.Fatal("original large display oracle missing")
	}
	var offsets []int
	// Use the same real scoped RPC seam, mutating authoritative state only after
	// the first actual response. This is not an artificial parser-only failure.
	interrupted := connectedapps.ManagedAccountInventory(func(c context.Context, revision int64, request contract.InventoryRequest, data []byte) (contract.InventoryPlan, error) {
		offsets = append(offsets, request.Offset)
		for _, private := range []string{"private-original-id", "private-provider-id", "private-account-alias"} {
			if strings.Contains(string(data), private) {
				t.Fatal("unneeded account identity entered candidate input")
			}
		}
		scoped, e := a.appsPrivateReadHost(host, revision)
		if e != nil {
			return contract.InventoryPlan{}, e
		}
		input, _ := json.Marshal(request)
		raw, e := scoped.ReadServiceStream(c, graph, contract.Inventory, input, rsi.InputData{ID: contract.InventoryInputID, Data: data})
		if e != nil {
			return contract.InventoryPlan{}, e
		}
		var plan contract.InventoryPlan
		if e = rsiplugin.DecodeResult(raw, &plan); e != nil {
			return plan, e
		}
		if request.Offset == 0 {
			if e = a.store.SaveApps(c, state, state.Revision); e != nil {
				t.Fatal(e)
			}
		}
		return plan, nil
	})
	projection, err = interrupted.Project(ctx, state, true)
	if !errors.Is(err, connectedapps.ErrConflict) || len(projection.Rows) != 0 || !reflect.DeepEqual(offsets, []int{0, contract.InventoryPageSize}) {
		t.Fatal("mixed-state or partial inventory released", offsets, len(projection.Rows), err)
	}
}
