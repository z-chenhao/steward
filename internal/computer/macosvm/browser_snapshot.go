package macosvm

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/computer"
)

//go:embed browser_snapshot_v1.mjs
var browserSnapshotSource string

func BrowserSnapshotResourceHash() string {
	h := sha256.Sum256([]byte("computer-browser-snapshot-v1\x00playwright-core@1.64.1\x00" + browserSnapshotSource))
	return hex.EncodeToString(h[:])
}

type browserSnapshotter struct{ provider *Provider }

func (p *Provider) BrowserSnapshotResource(hash string) (computer.BrowserSnapshotter, error) {
	if hash != BrowserSnapshotResourceHash() {
		return nil, computer.ErrCompatibility
	}
	return browserSnapshotter{provider: p}, nil
}

func (d browserSnapshotter) Snapshot(ctx context.Context, i computer.Instance, t computer.Target, lease string, o computer.ObserveOptions) (computer.BrowserSnapshot, error) {
	if t.Kind != "tab" || t.ID == "" || lease == "" {
		return computer.BrowserSnapshot{}, computer.ErrInvalid
	}
	p := d.provider
	if p.config.Browser == nil {
		return computer.BrowserSnapshot{}, computer.ErrUnavailable
	}
	g, ip, err := p.useGuest(ctx, i)
	if err != nil {
		return computer.BrowserSnapshot{}, err
	}
	cfg := p.config.Browser
	raw, _ := json.Marshal(map[string]any{"method": "observe", "snapshot": true, "args": map[string]any{"tabId": t.ID, "ax": o.Accessibility, "screenshot": o.Screenshot}, "port": cfg.Port, "module": cfg.Module})
	output, err := g.command(ctx, ip, quote(cfg.Node)+" --input-type=module -e "+quote(browserSnapshotSource)+" "+quote(string(raw)), false)
	if err != nil {
		return computer.BrowserSnapshot{}, err
	}
	v, err := decodeBrowserResponse(output)
	if err != nil {
		return computer.BrowserSnapshot{}, err
	}
	c, err := parseCapture(v)
	if err != nil {
		return computer.BrowserSnapshot{}, err
	}
	c.HasAX = o.Accessibility
	var source computer.BrowserSource
	encoded, _ := json.Marshal(v["source"])
	if json.Unmarshal(encoded, &source) != nil {
		return computer.BrowserSnapshot{}, computer.ErrInvalid
	}
	return computer.BrowserSnapshot{Capture: c, Source: source}, nil
}
