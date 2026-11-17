package fake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"path"
	"sync"
	"unicode/utf16"

	"github.com/z-chenhao/assistant/internal/computer"
)

// Driver is an explicit deterministic adapter Fake. It never substitutes for a
// configured real driver. Tests may inject partial failures and navigation.
type Driver struct {
	Mu            sync.Mutex
	Text          string
	Navigation    int
	Geometry      string
	Calls         []string
	Files         map[string][]byte
	FailInventory string
}

func (f *Driver) Inventory(ctx context.Context, i computer.Instance, kind string) ([]computer.Target, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls = append(f.Calls, "inventory:"+kind)
	if f.FailInventory == kind {
		return nil, computer.ErrUnavailable
	}
	switch kind {
	case "apps":
		return []computer.Target{{ID: "notes", Kind: "app", Name: "Notes"}}, nil
	case "windows":
		return []computer.Target{{ID: "notes-window", Kind: "window", Name: "Notes"}}, nil
	case "browsers":
		return []computer.Target{{ID: "chromium", Kind: "browser", Name: "Chromium"}}, nil
	case "tabs":
		return []computer.Target{{ID: "fixture-tab", Kind: "tab", Name: "Fixture", Browser: "chromium"}}, nil
	}
	return nil, computer.ErrInvalid
}
func (f *Driver) Observe(ctx context.Context, i computer.Instance, t computer.Target, lease string, options computer.ObserveOptions) (computer.Capture, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls = append(f.Calls, "observe:"+t.ID)
	return f.capture(options), nil
}

func (f *Driver) capture(options computer.ObserveOptions) computer.Capture {
	c := computer.Capture{HasAX: options.Accessibility, State: "[0] Notes " + f.Text + " [actions=increment]", Elements: []computer.Element{{Index: 0, Token: "fixture-element", Actions: []string{"increment"}}}, Width: 100, Height: 100, Geometry: f.Geometry, Navigation: fmt.Sprint(f.Navigation)}
	if options.Screenshot {
		var b bytes.Buffer
		png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 100, 100)))
		c.PNG = b.Bytes()
	}
	return c
}
func (f *Driver) Act(ctx context.Context, i computer.Instance, t computer.Target, lease, method string, m map[string]any) (any, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls = append(f.Calls, method)
	switch method {
	case "setValue":
		f.Text, _ = m["value"].(string)
	case "typeText", "paste":
		s, _ := m["text"].(string)
		f.Text += s
	case "goto", "reload":
		f.Navigation++
	case "createBrowserTab":
		return computer.Target{ID: "fixture-tab", Kind: "tab", Name: "Fixture", Browser: "chromium"}, nil
	case "playwright.textContent":
		return map[string]any{"value": f.Text}, nil
	case "playwright.count":
		return map[string]any{"value": 1}, nil
	case "playwright.fill":
		f.Text, _ = m["value"].(string)
	case "playwright.upload":
		data, ok := f.Files[m["path"].(string)]
		if !ok {
			return nil, computer.ErrIntegrity
		}
		name, _ := m["name"].(string)
		media, _ := m["mediaType"].(string)
		// Only the explicit facade-v1 wire fixture chooses historical defaults.
		// Managed upload receives the complete approved metadata from its Driver.
		if name == "" {
			name = path.Base(m["path"].(string))
			media = "application/octet-stream"
		}
		f.Text = fmt.Sprintf("uploaded:%s;media=%s;sha256=%x", name, media, sha256.Sum256(data))
	case "playwright.download":
		if f.Files == nil {
			f.Files = map[string][]byte{}
		}
		f.Files[m["path"].(string)] = []byte("fixture download")
	}
	return map[string]string{"effect": "confirmed"}, nil
}
func (f *Driver) ReadFile(ctx context.Context, i computer.Instance, path string) (io.ReadCloser, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	v, ok := f.Files[path]
	if !ok {
		return nil, computer.ErrIntegrity
	}
	return io.NopCloser(bytes.NewReader(v)), nil
}
func (f *Driver) WriteFile(ctx context.Context, i computer.Instance, path string, overwrite bool, r io.Reader) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Files == nil {
		f.Files = map[string][]byte{}
	}
	if _, ok := f.Files[path]; ok && !overwrite {
		return computer.ErrIntegrity
	}
	v, e := io.ReadAll(r)
	if e != nil {
		return e
	}
	f.Files[path] = v
	return nil
}

// Snapshot is the deterministic protocol peer of the fixed browser acquisition.
func (f *Driver) Snapshot(ctx context.Context, i computer.Instance, t computer.Target, lease string, o computer.ObserveOptions) (computer.BrowserSnapshot, error) {
	if t.Kind != "tab" || t.ID != "fixture-tab" || lease == "" {
		return computer.BrowserSnapshot{}, computer.ErrInvalid
	}
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Calls = append(f.Calls, "snapshot:"+t.ID)
	c := f.capture(o)
	s := computer.BrowserSource{}
	if o.Accessibility {
		all := utf16.Encode([]rune(f.Text))
		value := browserCodeUnits(all[:min(len(all), 4096)])
		s = computer.BrowserSource{Version: 1, Body: browserCodeUnits(all[:min(len(all), 16384)]), Rows: []computer.BrowserSourceRow{{Role: browserText("INPUT"), Aria: browserText("Fixture"), Value: &value}}}
		c.Elements = []computer.Element{{Index: 0, Token: "fixture-element", Role: "INPUT", Value: string(utf16.Decode(all[:min(len(all), 4096)]))}}
	} else {
		c.Elements = nil
		c.State = ""
	}
	return computer.BrowserSnapshot{Capture: c, Source: s}, nil
}
func browserText(s string) []byte {
	return browserCodeUnits(utf16.Encode([]rune(s)))
}
func browserCodeUnits(u []uint16) []byte {
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return b
}
