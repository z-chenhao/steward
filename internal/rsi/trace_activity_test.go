package rsi_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/observability"
	observabilitysqlite "github.com/z-chenhao/assistant/internal/observability/sqlite"
	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func traceActivityAlternatePackage(t *testing.T) string {
	t.Helper()
	directory := tracePackageVersion(t, "1.2.0", false)
	replacements := map[string]string{}
	for _, file := range []string{"plugin.go", "activity.go", "view/trace-activity.css", "view/trace.html", "view/trace-worker.js", "view/copy.json"} {
		source, err := filepath.Abs(filepath.Join("../plugin/traceinspector", file))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		code := string(raw)
		if file == "plugin.go" {
			code = strings.Replace(code, `Version: "1.1.0"`, `Version: "1.2.0"`, 1)
			code = strings.Replace(code, `o.Name == "Prepare context" || `, ``, 1)
			code = strings.ReplaceAll(code, `(mark.Phase == "first_text" || mark.Phase == "first_tool_call")`, `mark.Phase == "first_text"`)
			code = strings.ReplaceAll(code, `m.Phase == "first_text" || m.Phase == "first_tool_call"`, `m.Phase == "first_text"`)

			code = strings.ReplaceAll(code, `width := float64(4)`, `width := float64(8)`)
			code = strings.ReplaceAll(code, `max(4, min(1000-x`, `max(8, min(1000-x`)
			code = strings.ReplaceAll(code, `float64(end-o.Started)/float64(span)*1000`, `float64(end-o.Started)/float64(span)*900`)

		} else if file == "activity.go" {
			code = strings.ReplaceAll(code, "60_000_000", "120_000_000")
		} else if file == "view/trace-worker.js" {
			code = strings.ReplaceAll(code, `sections:['system','messages','tools','parameters']`, `sections:['parameters','system','messages','tools']`)
			code = strings.ReplaceAll(code, `expanded:{system:false,messages:'last'`, `expanded:{system:true,messages:'all'`)
			code = strings.ReplaceAll(code, "keep_filter:input.same_trace", "keep_filter:false")
			if err = os.WriteFile(filepath.Join(directory, "views", "trace-worker.js"), []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
		} else if file == "view/copy.json" {
			code = strings.Replace(code, `"chinese": "执行追踪"`, `"chinese": "独立版本轨迹"`, 1)
			if err = os.WriteFile(filepath.Join(directory, "views", "copy.json"), []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
		} else if file == "view/trace.html" {
			code = strings.Replace(code, `{{traceTr "Tracing"}}`, `{{traceTr "Versioned trace directory"}}`, 1)
			code = strings.Replace(code, `{{else if eq . "root"}}{{traceTr "Root Worker"}}`, `{{else if eq . "root"}}{{traceTr "Worker v2"}}`, 1)
			code = strings.ReplaceAll(code, `fraction .Count $activityMax 100`, `fraction .Count $activityMax 90`)
			code = strings.ReplaceAll(code, "trace-role-label", "trace-private-role-v2")
			code = strings.Replace(code, `{{else if eq . "metadata"}}{{traceTr "Details"}}`, `{{else if eq . "metadata"}}{{traceTr "Diagnostics v2"}}`, 1)
			if err = os.WriteFile(filepath.Join(directory, "views", "trace.html"), []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			code = strings.ReplaceAll(code, "height:88px", "height:96px")
			code = strings.ReplaceAll(code, "--timeline-width:44%", "--timeline-width:48%")
			code = strings.ReplaceAll(code, ".obs-tree-guides>span{width:12px", ".obs-tree-guides>span{width:16px")
			code = strings.ReplaceAll(code, ".obs-wire-reader{display:grid;gap:20px}", ".obs-wire-reader{display:grid;gap:24px}")
			if err = os.WriteFile(filepath.Join(directory, "views", "trace-activity.css"), []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if code == string(raw) {
			t.Fatal("alternate implementation unchanged", file)
		}
		replacement := filepath.Join(t.TempDir(), filepath.Base(file))
		if err = os.WriteFile(replacement, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[source] = replacement
	}
	overlay, err := json.Marshal(struct{ Replace map[string]string }{replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlayFile := filepath.Join(t.TempDir(), "overlay.json")
	if err = os.WriteFile(overlayFile, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-overlay", overlayFile, "-o", filepath.Join(directory, "plugin"), "../../cmd/plugins/trace-inspector").CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	manifestFile := filepath.Join(directory, "steward.plugin.json")
	raw, err := os.ReadFile(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err = rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest, err = rsi.Seal(directory, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return directory
}
func traceActivityOwnerPolicy(t *testing.T, id string, alternate bool) rsi.Policy {
	t.Helper()
	raw, err := os.ReadFile("builtin/trace-workbench-owner-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []rsi.Case
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if alternate {
		for i := range cases {
			if cases[i].Contract == contract.Workbench {
				var expected contract.WorkbenchResult
				if err = json.Unmarshal(cases[i].Expected, &expected); err != nil {
					t.Fatal(err)
				}
				expected.Program.Source = strings.ReplaceAll(expected.Program.Source, `sections:['system','messages','tools','parameters']`, `sections:['parameters','system','messages','tools']`)
				expected.Program.Source = strings.ReplaceAll(expected.Program.Source, `expanded:{system:false,messages:'last'`, `expanded:{system:true,messages:'all'`)
				expected.Program.Source = strings.ReplaceAll(expected.Program.Source, "keep_filter:input.same_trace", "keep_filter:false")
				expected.Program.Digest = rsiplugin.FileDigest([]byte(expected.Program.Source))
				expected.Stylesheet.Source = strings.ReplaceAll(expected.Stylesheet.Source, "height:88px", "height:96px")
				expected.Stylesheet.Source = strings.ReplaceAll(expected.Stylesheet.Source, "--timeline-width:44%", "--timeline-width:48%")
				expected.Stylesheet.Source = strings.ReplaceAll(expected.Stylesheet.Source, ".obs-tree-guides>span{width:12px", ".obs-tree-guides>span{width:16px")
				expected.Stylesheet.Source = strings.ReplaceAll(expected.Stylesheet.Source, ".obs-wire-reader{display:grid;gap:20px}", ".obs-wire-reader{display:grid;gap:24px}")
				expected.Stylesheet.Digest = rsiplugin.FileDigest([]byte(expected.Stylesheet.Source))
				expected.Template.Source = strings.Replace(expected.Template.Source, `{{traceTr "Tracing"}}`, `{{traceTr "Versioned trace directory"}}`, 1)
				expected.Template.Source = strings.Replace(expected.Template.Source, `{{else if eq . "root"}}{{traceTr "Root Worker"}}`, `{{else if eq . "root"}}{{traceTr "Worker v2"}}`, 1)
				expected.Template.Source = strings.ReplaceAll(expected.Template.Source, `fraction .Count $activityMax 100`, `fraction .Count $activityMax 90`)
				expected.Template.Source = strings.ReplaceAll(expected.Template.Source, "trace-role-label", "trace-private-role-v2")
				expected.Template.Source = strings.Replace(expected.Template.Source, `{{else if eq . "metadata"}}{{traceTr "Details"}}`, `{{else if eq . "metadata"}}{{traceTr "Diagnostics v2"}}`, 1)
				expected.Template.Digest = rsiplugin.FileDigest([]byte(expected.Template.Source))
				expected.Copy.Source = strings.Replace(expected.Copy.Source, `"chinese": "执行追踪"`, `"chinese": "独立版本轨迹"`, 1)
				expected.Copy.Digest = rsiplugin.FileDigest([]byte(expected.Copy.Source))
				cases[i].Expected, err = json.Marshal(expected)
				if err != nil {
					t.Fatal(err)
				}
			}
			if cases[i].Contract == contract.Activity {
				cases[i].Expected = json.RawMessage(`{"present":true,"start":1000000,"end":121000000,"base":0,"step":5000000}`)
			}
		}
	}
	for _, c := range []struct{ service, input, expected string }{{contract.Contract, `{"started":0,"now":1,"items":[],"timings":[]}`, `{"timeline":{"start":0,"end":1},"nodes":[],"tabs":["overview"]}`}, {contract.DiffContract, `{"section":"messages","before":[],"after_records":[]}`, `{"items":[]}`}, {contract.ToolContract, `{}`, `null`}} {
		cases = append(cases, rsi.Case{InstanceID: "trace-inspector", Contract: c.service, Input: json.RawMessage(`{"input_id":"observations"}`), Inputs: []rsi.InputData{{ID: "observations", Data: []byte(c.input)}}, Expected: json.RawMessage(c.expected)})
	}
	return rsi.Policy{ID: id, AllowedContracts: []string{contract.Contract, contract.DiffContract, contract.ToolContract, contract.Workbench, contract.Activity}, AllowedRequests: []string{"input.read"}, Cases: cases}
}

func TestTraceActivityActualDispatchUpgradeRollbackAndColdReferences(t *testing.T) {
	requireSandbox(t)
	ctx := context.Background()
	f := newFixture(t)
	if _, err := f.actor.Submit(ctx, "trace-activity", "private histogram evidence"); err != nil {
		t.Fatal(err)
	}
	if err := f.actor.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	inspector := f.store.Inspector()
	inspector.ConfigureTracePlugins(f.host.Catalog)
	original, err := inspector.Traces(ctx, observability.TraceQuery{})
	if err != nil || original.Total != 1 || len(original.Items) != 1 {
		t.Fatal("missing original trace", original.Total, err)
	}
	prepare := func(source string) rsi.ResolvedGraph {
		t.Helper()
		pkg, err := f.host.Install(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		graph, err := f.host.Prepare(ctx, rsiplugin.Graph{Protocol: rsiplugin.Protocol, Bindings: []rsiplugin.Binding{{InstanceID: "trace-inspector", PackageDigest: pkg.Digest, Config: json.RawMessage(`{}`), StateNamespace: "trace-inspector", StateVersion: 1}}})
		if err != nil {
			t.Fatal(err)
		}
		return graph
	}
	legacy := prepare(tracePackageVersion(t, "1.0.1", false))
	first := prepare(tracePackageVersion(t, "1.1.0", false))
	second := prepare(traceActivityAlternatePackage(t))
	originalPolicy := traceActivityOwnerPolicy(t, "trace-activity-original-owner", false)
	alternatePolicy := traceActivityOwnerPolicy(t, "trace-activity-alternate-owner", true)
	legacyPolicy := traceActivityOwnerPolicy(t, "trace-activity-legacy-owner", false)
	for i := range legacyPolicy.Cases {
		if legacyPolicy.Cases[i].Contract == contract.Contract {
			legacyPolicy.Cases[i].Expected = json.RawMessage(`{"nodes":[],"tabs":["overview"]}`)
		}
	}
	legacyCases := []rsi.Case{}
	for _, c := range legacyPolicy.Cases {
		if c.Contract != contract.Activity && c.Contract != contract.Workbench {
			legacyCases = append(legacyCases, c)
		}
	}
	legacyPolicy.Cases = legacyCases
	originalPolicy.Cases = append(originalPolicy.Cases, traceDisplayOwnerCase(false))
	originalPolicy.Cases = append(originalPolicy.Cases, traceMetricOwnerCase(false))
	alternatePolicy.Cases = append(alternatePolicy.Cases, traceDisplayOwnerCase(true))
	alternatePolicy.Cases = append(alternatePolicy.Cases, traceMetricOwnerCase(true))
	for _, policy := range []rsi.Policy{legacyPolicy, originalPolicy, alternatePolicy} {
		if err = f.host.Store.PutPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
	}
	activate := func(id, baseline, target, policy string) {
		t.Helper()
		if err := f.host.Propose(ctx, rsi.Candidate{ID: id, Baseline: baseline, Target: target, PolicyID: policy, Goal: "Independent histogram policy without changing records or counts"}); err != nil {
			t.Fatal(err)
		}
		decision, err := f.host.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.host.Activate(ctx, rsi.Activation{OperationID: id, Expected: baseline, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(value observability.Traces, want time.Duration) {
		t.Helper()
		if !reflect.DeepEqual(value.Items, original.Items) || value.Total != original.Total || len(value.Activity) == 0 {
			t.Fatal("activity policy changed source records", value)
		}
		var count int64
		for _, bucket := range value.Activity {
			count += bucket.Count
		}
		if count != value.Total {
			t.Fatal("policy lost authoritative counts", count, value.Total)
		}
		start, e := time.Parse(time.RFC3339Nano, value.Activity[0].At)
		if e != nil {
			t.Fatal(e)
		}
		end, e := time.Parse(time.RFC3339Nano, value.Activity[len(value.Activity)-1].Until)
		if e != nil || end.Sub(start) != want {
			t.Fatal("real policy window mismatch", start, end, want, e)
		}
	}
	styleBytes, e := os.ReadFile("../plugin/traceinspector/view/trace-activity.css")
	if e != nil {
		t.Fatal(e)
	}
	styleOriginal := string(styleBytes)
	templateBytes, e := os.ReadFile("../plugin/traceinspector/view/trace.html")
	if e != nil {
		t.Fatal(e)
	}
	templateOriginal := string(templateBytes)
	templateAlternate := strings.Replace(templateOriginal, `{{traceTr "Tracing"}}`, `{{traceTr "Versioned trace directory"}}`, 1)
	templateAlternate = strings.Replace(templateAlternate, `{{else if eq . "root"}}{{traceTr "Root Worker"}}`, `{{else if eq . "root"}}{{traceTr "Worker v2"}}`, 1)
	templateAlternate = strings.ReplaceAll(templateAlternate, `fraction .Count $activityMax 100`, `fraction .Count $activityMax 90`)
	templateAlternate = strings.ReplaceAll(templateAlternate, "trace-role-label", "trace-private-role-v2")
	templateAlternate = strings.Replace(templateAlternate, `{{else if eq . "metadata"}}{{traceTr "Details"}}`, `{{else if eq . "metadata"}}{{traceTr "Diagnostics v2"}}`, 1)
	styleAlternate := strings.ReplaceAll(styleOriginal, "height:88px", "height:96px")
	styleAlternate = strings.ReplaceAll(styleAlternate, "--timeline-width:44%", "--timeline-width:48%")
	styleAlternate = strings.ReplaceAll(styleAlternate, ".obs-tree-guides>span{width:12px", ".obs-tree-guides>span{width:16px")
	styleAlternate = strings.ReplaceAll(styleAlternate, ".obs-wire-reader{display:grid;gap:20px}", ".obs-wire-reader{display:grid;gap:24px}")
	programBytes, err := os.ReadFile("../plugin/traceinspector/view/trace-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	programOriginal := string(programBytes)
	programAlternate := programOriginal
	programAlternate = strings.ReplaceAll(programAlternate, `sections:['system','messages','tools','parameters']`, `sections:['parameters','system','messages','tools']`)
	programAlternate = strings.ReplaceAll(programAlternate, `expanded:{system:false,messages:'last'`, `expanded:{system:true,messages:'all'`)
	programAlternate = strings.ReplaceAll(programAlternate, "keep_filter:input.same_trace", "keep_filter:false")
	checkStyle := func(reader *observabilitysqlite.Inspector, revision, want string) {
		t.Helper()
		bundle, e := reader.TraceWorkbenchProgram(ctx, revision)
		if e != nil || bundle.Stylesheet != want {
			t.Fatal("frozen owning stylesheet mismatch", e)
		}
		expectedTemplate := templateOriginal
		expectedProgram := programOriginal
		if want == styleAlternate {
			expectedTemplate = templateAlternate
			expectedProgram = programAlternate
		}
		if bundle.ViewSource != expectedTemplate || bundle.Source != expectedProgram {
			t.Fatal("frozen owning template or label policy mismatch")
		}

		expectedChinese := "执行追踪"
		if want == styleAlternate {
			expectedChinese = "独立版本轨迹"
		}
		foundCopy := false
		for _, entry := range bundle.Copy {
			if entry.English == "Tracing" {
				foundCopy = entry.Chinese == expectedChinese
			}
		}
		if !foundCopy {
			t.Fatal("frozen owning lexicon changed", expectedChinese)
		}
		selection, e := reader.TraceWorkbenchSelection(ctx, revision)
		if e != nil || selection.ViewSource != bundle.ViewSource || !reflect.DeepEqual(selection.Copy, bundle.Copy) {
			t.Fatal("selection lost owning template", e)
		}
	}
	checkDisplay := func(reader *observabilitysqlite.Inspector, revision string, alternate bool) {
		t.Helper()
		tree, view, e := reader.ProjectedObservations(ctx, observability.ObservationQuery{RunID: original.Items[0].ID, ReaderRevision: revision})
		if e != nil || view == nil || view.Timeline == nil || len(view.Rows) != len(tree.Items) {
			t.Fatal("actual display projection missing", e)
		}
		metric := traceMetricOwnerCase(alternate)
		raw, e := f.host.ReadService(ctx, revision, contract.Contract, metric.Input, metric.Inputs)
		if e != nil {
			t.Fatal("frozen actual metric policy unavailable", e)
		}
		got, e := rsiplugin.Canonical(raw)
		if e != nil {
			t.Fatal(e)
		}
		want, e := rsiplugin.Canonical(metric.Expected)
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("independent metric interpretation changed", string(raw), e)
		}
		contexts := 0
		for _, row := range view.Rows {
			if row.Name == "Prepare context" {
				contexts++
				want := float64(4)
				if alternate {
					want = 8
				}
				if row.Primary != alternate || row.Width != want {
					t.Fatal("owning display policy was overwritten", row, alternate)
				}
			}
		}
		if contexts == 0 {
			t.Fatal("real context receipt missing from display fixture")
		}
	}
	activate("activity-legacy", "", legacy.Revision, legacyPolicy.ID)
	legacyValue, err := inspector.Traces(ctx, observability.TraceQuery{})
	if err != nil || !reflect.DeepEqual(legacyValue, original) {
		t.Fatal("old1.0.1 active graph lost original histogram", err)
	}
	activate("activity-first", legacy.Revision, first.Revision, originalPolicy.ID)
	current, err := inspector.Traces(ctx, observability.TraceQuery{})
	if err != nil || !reflect.DeepEqual(current, original) {
		t.Fatal("managed plan changed original output", err)
	}
	check(current, time.Minute)
	checkStyle(inspector, first.Revision, styleOriginal)
	checkDisplay(inspector, first.Revision, false)
	activate("activity-upgrade", first.Revision, second.Revision, alternatePolicy.ID)
	upgraded, err := inspector.Traces(ctx, observability.TraceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	check(upgraded, 2*time.Minute)
	checkStyle(inspector, second.Revision, styleAlternate)
	checkDisplay(inspector, second.Revision, true)
	checkStyle(inspector, first.Revision, styleOriginal)
	checkDisplay(inspector, first.Revision, false)
	pinned, err := inspector.Traces(ctx, observability.TraceQuery{ReaderRevision: first.Revision})
	if err != nil || !reflect.DeepEqual(pinned, current) {
		t.Fatal("upgrade changed old graph's actual histogram", err)
	}
	at, err := time.Parse(time.RFC3339Nano, original.Items[0].CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	explicit := observability.TraceQuery{}
	explicit.Since = at.Add(-time.Second)
	explicit.Until = at.Add(time.Second)
	scoped, err := inspector.Traces(ctx, explicit)
	if err != nil {
		t.Fatal(err)
	}
	check(scoped, 2*time.Second)
	activate("activity-rollback", second.Revision, first.Revision, originalPolicy.ID)
	rolledBack, err := inspector.Traces(ctx, observability.TraceQuery{})
	if err != nil || !reflect.DeepEqual(rolledBack, current) {
		t.Fatal("rollback did not restore exact bins", err)
	}
	checkStyle(inspector, first.Revision, styleOriginal)
	checkDisplay(inspector, first.Revision, false)
	if err = f.host.Store.Retire(ctx, second.Graph.Bindings[0].PackageDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = f.host.Store.Deactivate(ctx, "activity-disabled", first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = inspector.Traces(ctx, observability.TraceQuery{}); err == nil {
		t.Fatal("deactivation silently used native policy")
	}
	before, err := f.host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledger := func() []int {
		t.Helper()
		values := make([]int, 3)
		for i, table := range []string{"rsi_call", "rsi_state", "rsi_audit"} {
			if err := f.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&values[i]); err != nil {
				t.Fatal(err)
			}
		}
		return values
	}
	beforeLedger := ledger()
	for _, retained := range []struct {
		revision string
		span     time.Duration
	}{{legacy.Revision, time.Minute}, {first.Revision, time.Minute}, {second.Revision, 2 * time.Minute}} {
		// Separate read-only pools and catalog: the first read after open is the
		// original explicit reference. There is no select, rewrite or repair action.
		cold, err := observabilitysqlite.Open(filepath.Join(f.root, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		catalog := rsi.NewCatalog(f.root)
		catalog.Launcher = fixtureExecutable
		cold.ConfigureTracePlugins(catalog)
		value, readErr := cold.Traces(ctx, observability.TraceQuery{ReaderRevision: retained.revision})
		cold.Close()
		if readErr != nil {
			t.Fatal("retained graph cold read", readErr)
		}
		check(value, retained.span)
		if retained.revision != legacy.Revision {
			coldStyle, e := observabilitysqlite.Open(filepath.Join(f.root, "state.db"))
			if e != nil {
				t.Fatal(e)
			}
			catalog := rsi.NewCatalog(f.root)
			catalog.Launcher = fixtureExecutable
			coldStyle.ConfigureTracePlugins(catalog)
			want := styleOriginal
			if retained.revision == second.Revision {
				want = styleAlternate
			}
			checkStyle(coldStyle, retained.revision, want)
			checkDisplay(coldStyle, retained.revision, retained.revision == second.Revision)
			coldStyle.Close()
		}
	}
	after, err := f.host.Store.Status(ctx)
	if err != nil || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeLedger, ledger()) {
		t.Fatal("pure cold inspection changed control or ledger", err)
	}
	if err = f.host.Store.Quarantine(ctx, second.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = inspector.Traces(ctx, observability.TraceQuery{ReaderRevision: second.Revision}); err == nil {
		t.Fatal("Guardian did not revoke frozen activity policy")
	}
	if _, err = inspector.TraceWorkbenchProgram(ctx, second.Revision); err == nil {
		t.Fatal("Guardian did not revoke owning stylesheet")
	}
	// Independent raw observation inspection remains available to the Host.
	if raw, err := inspector.Observations(ctx, observability.ObservationQuery{RunID: original.Items[0].ID}); err != nil || len(raw.Items) == 0 {
		t.Fatal("display deactivation changed authoritative diagnostics", err)
	}
}

// Independently specified Guardian facts exercise the changed visibility, point
// floor and running lane in the actual sealed process, not a native proxy.
func traceDisplayOwnerCase(alternate bool) rsi.Case {
	input := []byte(`{"started":1000000,"now":2000000,"selected_id":"root","items":[{"id":"root","type":"agent","name":"Main","status":"running","parent":"","started":1000000},{"id":"context","type":"tool","name":"Prepare context","status":"running","parent":"root","started":1200000},{"id":"point","type":"chain","name":"Prepare context","status":"saved","parent":"root","started":1500000,"ended":1500000}],"timings":[]}`)
	expected := `{"timeline":{"start":1000000,"end":2000000},"nodes":[{"id":"root","primary":true,"running":true,"depth":0,"parent_outside":false,"has_children":true,"guides":[],"x":0,"width":1000},{"id":"context","running":true,"depth":1,"parent_outside":false,"has_children":false,"guides":[true],"x":200,"width":800},{"id":"point","depth":1,"parent_outside":false,"has_children":false,"guides":[false],"x":500,"width":4}],"tabs":["overview"]}`
	if alternate {
		expected = `{"timeline":{"start":1000000,"end":2000000},"nodes":[{"id":"root","primary":true,"primary_has_children":true,"running":true,"depth":0,"parent_outside":false,"has_children":true,"guides":[],"x":0,"width":900},{"id":"context","primary":true,"running":true,"depth":1,"parent_outside":false,"has_children":false,"guides":[true],"x":200,"width":720},{"id":"point","primary":true,"depth":1,"parent_outside":false,"has_children":false,"guides":[false],"x":500,"width":8}],"tabs":["overview"]}`
	}
	return rsi.Case{InstanceID: "trace-inspector", Contract: contract.Contract, Input: json.RawMessage(`{"input_id":"display-facts"}`), Inputs: []rsi.InputData{{ID: "display-facts", Data: input}}, Expected: json.RawMessage(expected)}
}

// First meaningful output and first text are separate legitimate display
// definitions. Real independent binaries select them; source events stay fixed.
func traceMetricOwnerCase(textOnly bool) rsi.Case {
	input := []byte(`{"started":1000000,"ended":2400000,"now":3000000,"selected_id":"generation:metric","selected_type":"generation","selected_name":"Model call","selected_status":"completed","usage":{"known_usage":1,"output_tokens":100},"items":[{"id":"generation:metric","type":"generation","name":"Model call","status":"completed","parent":"","started":1000000,"ended":2400000}],"timings":[{"attempt":"a","phase":"started","at":1000000},{"attempt":"a","phase":"first_tool_call","at":1200000},{"attempt":"a","phase":"first_text","at":1400000},{"attempt":"a","phase":"stream_terminal","at":2400000}]}`)
	expected := `{"timeline":{"start":1000000,"end":2400000},"nodes":[{"id":"generation:metric","primary":true,"depth":0,"parent_outside":false,"has_children":false,"guides":[],"x":0,"width":1000}],"ttft_ms":200,"first_output_at":1200000,"output_rate":83.33333333333333,"tabs":["overview","context","wire","diff"]}`
	if textOnly {
		expected = `{"timeline":{"start":1000000,"end":2400000},"nodes":[{"id":"generation:metric","primary":true,"depth":0,"parent_outside":false,"has_children":false,"guides":[],"x":0,"width":1000}],"ttft_ms":400,"first_output_at":1400000,"output_rate":100,"tabs":["overview","context","wire","diff"]}`
	}
	return rsi.Case{InstanceID: "trace-inspector", Contract: contract.Contract, Input: json.RawMessage(`{"input_id":"metric-facts"}`), Inputs: []rsi.InputData{{ID: "metric-facts", Data: input}}, Expected: json.RawMessage(expected)}
}
