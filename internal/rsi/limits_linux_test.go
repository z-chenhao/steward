//go:build linux

package rsi_test

import (
	"context"
	"encoding/json"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"testing"
)

func TestPluginNativeLauncherRetainsHardEnvelopeAndCannotRaiseIt(t *testing.T) {
	requireSandbox(t)
	f := newFixture(t)
	pkg := install(t, f, manifest())
	binding := rsiplugin.Binding{InstanceID: "test", PackageDigest: pkg.Digest, Config: json.RawMessage(`{"tag":""}`), StateNamespace: "test", StateVersion: 1}
	var output rsiplugin.Result
	err := (rsi.Runner{Catalog: f.host.Catalog}).Invoke(context.Background(), binding, "call", rsiplugin.Call{Contract: "steward.echo/1", Input: json.RawMessage(`{"text":"kernel-limits"}`)}, &output)
	if err != nil {
		t.Fatal(err)
	}
	var limits testProcessLimits
	if json.Unmarshal(output.Output, &limits) != nil || limits.CPUSoft != 30 || limits.CPUHard != 30 || limits.FileSoft != 16<<20 || limits.FileHard != 16<<20 || limits.FDSoft != 128 || limits.FDHard != 128 || limits.ASSoft != 2<<30 || limits.ASHard != 2<<30 || !limits.RaiseDenied {
		t.Fatal("native limiter changed or candidate raised its hard envelope", limits)
	}
}
