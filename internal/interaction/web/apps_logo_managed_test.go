package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/appslogos/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type logoRegistration struct{ Slug, Source string }
type logoStep struct {
	Register   []logoRegistration
	AgeSeconds int64
	Cancel     bool
}
type logoHTTPResult struct {
	Code                                      int
	Kind, ETag, CacheControl, CSP, BodySHA256 string
	Bytes, Entries                            int
}
type logoHTTPFixture struct {
	Name, Slug, BodyKind string
	Status               int
	Steps                []logoStep
	Expected             []logoHTTPResult
	Requests             []string
}

func TestAppsLogoManagedOriginalHTTPBytesSourcesAndCache(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	carrier := filepath.Join(t.TempDir(), "steward")
	for _, target := range []struct{ name, path string }{{filepath.Join(dir, "plugin"), "../../../cmd/plugins/apps-logos"}, {carrier, "../../../cmd/steward"}} {
		if out, err := exec.Command("go", "build", "-o", target.name, target.path).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	raw, err := os.ReadFile("../../../plugins/apps-logos/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest, err = rsi.Seal(dir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	catalog := rsi.Catalog{Root: t.TempDir(), Launcher: carrier}
	pkg, err := catalog.Install(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	runner := rsi.Runner{Catalog: catalog}
	policy := ManagedAppLogos("original-native-fixture", func(c context.Context, input contract.Request) (contract.Plan, error) {
		raw, err := json.Marshal(input)
		if err != nil {
			return contract.Plan{}, err
		}
		var result rsiplugin.Result
		err = runner.Invoke(c, rsiplugin.Binding{InstanceID: "logos", PackageDigest: pkg.Digest}, "call", rsiplugin.Call{OperationID: "owner-read", Contract: contract.Logo, Input: raw, State: rsiplugin.State{Value: json.RawMessage(`{}`)}}, &result)
		if err != nil {
			return contract.Plan{}, err
		}
		var plan contract.Plan
		err = rsiplugin.DecodeResult(result.Output, &plan)
		return plan, err
	}, func(c context.Context) error { return c.Err() })
	raw, err = os.ReadFile("../../../tests/fixtures/plugins/apps-logos-native-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []logoHTTPFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 82 {
		t.Fatal("original fixture count", len(fixtures))
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			body := []byte(`<svg viewBox="0 0 10 10"><path fill="#ea4335" d="M0 0h10v10H0z"/></svg>`)
			switch f.BodyKind {
			case "oversize":
				body = []byte(strings.Repeat("x", maxAppLogo+1))
			case "html":
				body = []byte("<html>not a logo</html>")
			case "active-svg":
				body = []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="evil()"><script>evil()</script><path fill="red" d="M0 0h10v10z"/></svg>`)
			case "png", "huge-png":
				width, height := 2, 2
				if f.BodyKind == "huge-png" {
					width, height = 4097, 1024
				}
				var buf bytes.Buffer
				if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
					t.Fatal(err)
				}
				body = buf.Bytes()
			}
			requests := []string{}
			results := []logoHTTPResult{}
			s := languageServer()
			s.Logos = func(context.Context, string) (AppLogoPolicy, error) { return policy, nil }
			s.appLogos.client = &http.Client{Transport: logoTransport(func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.Method+" "+r.URL.String()+" accept="+r.Header.Get("Accept"))
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("x-api-key") != "" {
					t.Fatal("candidate/source received credential")
				}
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				status := f.Status
				if status == 0 {
					status = 200
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}
			for _, step := range f.Steps {
				for _, reg := range step.Register {
					s.appLogos.registerForRevision(policy.revision, reg.Slug, reg.Source)
				}
				if step.AgeSeconds > 0 {
					for key, v := range s.appLogos.entries {
						v.at = time.Now().Add(-time.Duration(step.AgeSeconds) * time.Second)
						s.appLogos.entries[key] = v
					}
				}
				r := languageRequest("GET", "/apps/logo/"+f.Slug, "en", "")
				if step.Cancel {
					c, cancel := context.WithCancel(r.Context())
					cancel()
					r = r.WithContext(c)
				}
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				hash := sha256.Sum256(w.Body.Bytes())
				results = append(results, logoHTTPResult{Code: w.Code, Kind: w.Header().Get("Content-Type"), ETag: w.Header().Get("ETag"), CacheControl: w.Header().Get("Cache-Control"), CSP: w.Header().Get("Content-Security-Policy"), BodySHA256: hex.EncodeToString(hash[:]), Bytes: w.Body.Len(), Entries: len(s.appLogos.entries)})
			}
			if !reflect.DeepEqual(results, f.Expected) {
				t.Fatalf("original HTTP/image/cache changed\ngot %#v\nwant %#v", results, f.Expected)
			}
			if f.Name == "cancelled-read" {
				if len(requests) != 0 {
					t.Fatal("cancelled managed read touched source")
				}
			} else if !reflect.DeepEqual(requests, f.Requests) {
				t.Fatalf("source trace changed %v != %v", requests, f.Requests)
			}
		})
	}
}

func TestAppsLogoHostRejectsInventedCacheSourceBytesAndRevocation(t *testing.T) {
	ctx := context.Background()
	t.Run("source revoked before bytes accepted", func(t *testing.T) {
		revoked := false
		cache := appLogoCache{client: &http.Client{Transport: logoTransport(func(*http.Request) (*http.Response, error) {
			revoked = true
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<svg><path d="M0 0h1v1z"/></svg>`))}, nil
		})}}
		cache.registerForRevision("owned", "original", "")
		p := ManagedAppLogos("owned", func(context.Context, contract.Request) (contract.Plan, error) {
			return contract.Plan{Kind: "provider", CacheSeconds: 86400}, nil
		}, func(context.Context) error {
			if revoked {
				return context.Canceled
			}
			return nil
		})
		if _, _, _, err := cache.getManaged(ctx, "original", p); err == nil || len(cache.entries) != 0 {
			t.Fatal("revoked source image released or cached")
		}
	})
	t.Run("expired cache cannot be invented as current", func(t *testing.T) {
		cache := appLogoCache{entries: map[string]cachedAppLogo{"owned|original": {body: []byte("original"), kind: "image/svg+xml", at: time.Now().Add(-25 * time.Hour)}}}
		p := ManagedAppLogos("owned", func(context.Context, contract.Request) (contract.Plan, error) {
			return contract.Plan{Kind: "cached", CacheSeconds: 86400}, nil
		}, func(context.Context) error { return nil })
		if _, _, _, err := cache.getManaged(ctx, "original", p); err == nil {
			t.Fatal("expired cache accepted")
		}
	})
	for _, plan := range []contract.Plan{{Kind: "cached", CacheSeconds: 86400}, {Kind: "provider", CacheSeconds: 86400}, {Kind: "bundled", Body: []byte("<html/>"), CacheSeconds: 86400}, {Kind: "bundled", Body: bytes.Repeat([]byte("x"), maxAppLogo+1), CacheSeconds: 86400}, {Kind: "bundled", CacheSeconds: 86401}} {
		cache := appLogoCache{}
		p := ManagedAppLogos("owned", func(context.Context, contract.Request) (contract.Plan, error) { return plan, nil }, func(context.Context) error { return nil })
		if _, _, _, err := cache.getManaged(context.Background(), "original", p); err == nil || len(cache.entries) != 0 {
			t.Fatal("invented result accepted", plan.Kind)
		}
	}
	checks := 0
	cache := appLogoCache{}
	p := ManagedAppLogos("owned", func(context.Context, contract.Request) (contract.Plan, error) {
		return contract.Plan{Kind: "bundled", Body: []byte(`<svg><path d="M0 0h1v1z"/></svg>`), CacheSeconds: 86400}, nil
	}, func(context.Context) error { checks++; return context.Canceled })
	if _, _, _, err := cache.getManaged(context.Background(), "original", p); err == nil || len(cache.entries) != 0 || checks != 1 {
		t.Fatal("revoked bytes cached/released")
	}
}
