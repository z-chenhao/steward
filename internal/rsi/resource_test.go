package rsi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func resourceFixture(t *testing.T, duration int) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	m := manifest()
	m.Runtime.TimeoutMS = duration
	m.Hooks = nil
	m.Tools = nil
	m.Requests = []string{"resource.call"}
	p := install(t, f, m)
	g := graph(t, f, p, "")
	activate(t, f, g, "resource")
	return f, g.Revision
}
func fixtureResource(invoke func(context.Context, string, json.RawMessage) (json.RawMessage, error)) rsi.Resource {
	schema := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","maxLength":12000000}},"required":["text"],"additionalProperties":false}`)
	return rsi.Resource{Contract: "steward.fixture-resource/1", InputSchema: schema, OutputSchema: schema, Invoke: invoke}
}
func TestResourceDomainDriveUsesRealProcessWithoutAnotherPendingLedger(t *testing.T) {
	requireSandbox(t)
	f, revision := resourceFixture(t, 30000)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, mode := range []string{"resource-repeat", "resource-large"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			port := fixtureResource(func(_ context.Context, key string, input json.RawMessage) (json.RawMessage, error) {
				calls++
				if key != "original" {
					t.Fatal(key)
				}
				return input, nil
			})
			input, _ := json.Marshal(map[string]string{"text": mode, "path": "original private bytes"})
			out, err := f.host.DriveService(ctx, revision, "steward.echo/1", input, nil, func(context.Context) error { return nil }, []rsi.Resource{port})
			want := len("original private bytes")
			if mode == "resource-large" {
				want = len(strings.Repeat("合同<>🧭", 2100000))
				if want <= rsiplugin.MaxInputBytes {
					t.Fatal("fixture did not cross pure input limit")
				}
			}
			var value struct {
				Text string `json:"text"`
			}
			if err != nil || json.Unmarshal(out, &value) != nil || value.Text != strconv.Itoa(want) || calls != 1 {
				t.Fatal(string(out), calls, err)
			}
		})
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM rsi_call`).Scan(&count); err != nil || count != 0 {
		t.Fatal("domain drive created a second opaque pending ledger", count, err)
	}
}

func TestResourceDomainDriveRejectsUndeclaredConflictAndRevokedRelease(t *testing.T) {
	requireSandbox(t)
	f, revision := resourceFixture(t, 30000)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, mode := range []string{"resource-unknown", "resource-conflict", "resource-revoked"} {
		t.Run(mode, func(t *testing.T) {
			var allowed atomic.Bool
			allowed.Store(true)
			calls := 0
			check := func(c context.Context) error {
				if err := c.Err(); err != nil {
					return err
				}
				if !allowed.Load() {
					return rsi.ErrRevoked
				}
				return nil
			}
			port := fixtureResource(func(_ context.Context, _ string, input json.RawMessage) (json.RawMessage, error) {
				calls++
				if mode == "resource-revoked" {
					allowed.Store(false)
					return json.RawMessage(`{"text":"PRIVATE_HOST_ONLY_NEW_RESULT"}`), nil
				}
				return input, nil
			})
			input, _ := json.Marshal(map[string]string{"text": mode, "path": "PRIVATE_RESULT"})
			out, err := f.host.DriveService(ctx, revision, "steward.echo/1", input, nil, check, []rsi.Resource{port})
			if len(out) != 0 || err == nil {
				t.Fatal("invalid resource produced output", string(out), err)
			}
			if mode == "resource-unknown" && (!errors.Is(err, rsi.ErrRevoked) || calls != 0) {
				t.Fatal(calls, err)
			}
			if mode == "resource-conflict" && (!errors.Is(err, rsi.ErrConflict) || !errors.Is(err, rsi.ErrUnknown) || calls != 1) {
				t.Fatal("changed duplicate was redispatched", calls, err)
			}
			if mode == "resource-revoked" && (!errors.Is(err, rsi.ErrRevoked) || calls != 1) {
				t.Fatal("late revocation did not dominate", calls, err)
			}
		})
	}
	input := json.RawMessage(`{"text":"resource","path":"private"}`)
	if out, err := f.host.ReadService(ctx, revision, "steward.echo/1", input, nil); err == nil || len(out) != 0 {
		t.Fatal("pure call acquired domain resources", string(out), err)
	}
	calls := 0
	port := fixtureResource(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"text":"leak"}`), nil
	})
	if out, err := f.host.DriveService(context.Background(), revision, "steward.echo/1", input, nil, func(context.Context) error { return nil }, []rsi.Resource{port}); err == nil || len(out) != 0 || calls != 0 {
		t.Fatal("unbounded owner context accepted", calls, err)
	}
}

func TestResourceDomainDeadlineExcludesOnlyTrustedIOAndRetainsOriginalWallLimit(t *testing.T) {
	requireSandbox(t)
	f, revision := resourceFixture(t, 750)
	for _, test := range []struct {
		name, mode string
		wall, wait time.Duration
		success    bool
	}{
		{"trusted-io", "resource", 4 * time.Second, 1200 * time.Millisecond, true},
		{"original-wall", "resource", 300 * time.Millisecond, 1200 * time.Millisecond, false},
		{"cumulative-plugin-budget", "resource-budget", 4 * time.Second, 1200 * time.Millisecond, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), test.wall)
			defer cancel()
			calls := 0
			port := fixtureResource(func(c context.Context, _ string, input json.RawMessage) (json.RawMessage, error) {
				calls++
				select {
				case <-c.Done():
					return nil, c.Err()
				case <-time.After(test.wait):
					return input, nil
				}
			})
			input, _ := json.Marshal(map[string]string{"text": test.mode, "path": "source"})
			out, err := f.host.DriveService(ctx, revision, "steward.echo/1", input, nil, func(c context.Context) error { return c.Err() }, []rsi.Resource{port})
			if calls != 1 {
				t.Fatal("resource was not dispatched once", calls, err)
			}
			if test.success {
				if err != nil || string(out) != `{"text":"6"}` {
					t.Fatal(string(out), err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || len(out) != 0 {
				t.Fatal("deadline changed or budget restarted", string(out), err)
			}
		})
	}
}

func TestResourceVerificationRequiresExactCompleteOwnerTrace(t *testing.T) {
	requireSandbox(t)
	f, revision := resourceFixture(t, 30000)
	ctx := context.Background()
	selected, err := f.host.Store.Graph(ctx, revision)
	if err != nil {
		t.Fatal(err)
	}
	selected.Graph.Bindings[0].StateNamespace = "verify-resources"
	candidate, err := f.host.Prepare(ctx, selected.Graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"complete", "wrong-input", "unconsumed"} {
		t.Run(mode, func(t *testing.T) {
			fixture := rsi.ResourceFixture{Contract: "steward.fixture-resource/1", Key: "original", Input: json.RawMessage(`{"text":"private"}`), Output: json.RawMessage(`{"text":"owner proof"}`)}
			if mode == "wrong-input" {
				fixture.Input = json.RawMessage(`{"text":"another query"}`)
			}
			resources := []rsi.ResourceFixture{fixture}
			if mode == "unconsumed" {
				extra := fixture
				extra.Key = "never-requested"
				resources = append(resources, extra)
			}
			policy := rsi.Policy{ID: "resource-trace-" + mode, AllowedContracts: []string{"steward.echo/1"}, AllowedRequests: []string{"resource.call"}, Cases: []rsi.Case{{InstanceID: "test", Contract: "steward.echo/1", Input: json.RawMessage(`{"text":"resource","path":"private"}`), Expected: json.RawMessage(`{"text":"11"}`), Resources: resources}}}

			if err = f.host.Store.PutPolicy(ctx, policy); err != nil {
				t.Fatal(err)
			}
			if err = f.host.Propose(ctx, rsi.Candidate{ID: policy.ID, Baseline: revision, Target: candidate.Revision, PolicyID: policy.ID, Goal: "reject incomplete owner resource evidence"}); err != nil {
				t.Fatal(err)
			}
			_, err = f.host.Verify(ctx, policy.ID)
			if mode == "complete" {
				if err != nil {
					t.Fatal("valid owner resource trace rejected", err)
				}
			} else if !errors.Is(err, rsi.ErrVerification) {
				t.Fatal("invalid resource trace passed", err)
			}
			status, e := f.host.Store.Status(ctx)
			if e != nil || status.Revision != revision {
				t.Fatal("verification changed selection", status, e)
			}
		})
	}
}
