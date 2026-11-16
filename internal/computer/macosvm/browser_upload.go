package macosvm

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/computer"
)

//go:embed browser_upload_v1.mjs
var browserUploadSource string

// This resource is separate from the unchanged facade-v1 UseResourceHash.
// A retained graph binds its exact bytes, not whichever Driver is newest.
func BrowserUploadResourceHash() string {
	h := sha256.New()
	h.Write([]byte("computer-browser-upload-v1\x00playwright-core@1.64.1\x00"))
	h.Write([]byte(browserUploadSource))
	h.Write([]byte{0})
	h.Write([]byte(transferSource))
	return hex.EncodeToString(h.Sum(nil))
}

type browserUploader struct{ provider *Provider }

// BrowserUploadResource selects only this Host-owned fixed primitive. Config
// data cannot register a program or mutate the shared Provider's Driver choice.
func (p *Provider) BrowserUploadResource(hash string) (computer.BrowserUploader, error) {
	if hash != BrowserUploadResourceHash() {
		return nil, computer.ErrCompatibility
	}
	return browserUploader{provider: p}, nil
}

func (d browserUploader) Upload(ctx context.Context, i computer.Instance, target computer.Target, lease string, a computer.BrowserUploadRequest) (any, error) {
	if target.Kind != "tab" || target.ID == "" || lease == "" || a.Validate() != nil {
		return nil, computer.ErrInvalid
	}
	p := d.provider
	if p.config.Browser == nil {
		return nil, computer.ErrUnavailable
	}
	g, ip, err := p.useGuest(ctx, i)
	if err != nil {
		return nil, err
	}
	cfg := p.config.Browser
	helper := sha256.Sum256([]byte(transferSource))
	raw, _ := json.Marshal(map[string]any{"method": "playwright.upload", "args": map[string]any{"tabId": target.ID, "selector": a.Selector, "timeoutMs": a.TimeoutMS, "expectedNavigation": a.Navigation, "path": a.Path, "name": a.Name, "mediaType": a.MediaType}, "port": cfg.Port, "module": cfg.Module, "root": p.transferRoot(), "helper_sha256": hex.EncodeToString(helper[:])})
	output, err := g.command(ctx, ip, quote(cfg.Node)+" --input-type=module -e "+quote(browserUploadSource)+" "+quote(string(raw)), false)
	if err != nil {
		return nil, computer.ErrUnknown
	}
	return decodeBrowserResponse(output)
}
