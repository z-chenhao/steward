package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/plugin/webimage"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func webImageOwnerCase(version string) rsi.Case {
	original := image.NewRGBA(image.Rect(0, 0, 2, 3))
	var source bytes.Buffer
	if err := png.Encode(&source, original); err != nil {
		panic(err)
	}
	// The accepted V1 fixture is an opaque white conversion of transparent pixels.
	white := image.NewRGBA(original.Bounds())
	draw.Draw(white, white.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	quality, text, kind := 85, "Image fetched (2 x 3)", "image/jpeg"
	if version == "1.20.0" {
		quality, text = 70, "Evidence image (2 x 3)"
	}
	var converted bytes.Buffer
	if version == "1.21.0" {
		kind = "image/png"
		if err := png.Encode(&converted, white); err != nil {
			panic(err)
		}
	} else if err := jpeg.Encode(&converted, white, &jpeg.Options{Quality: quality}); err != nil {
		panic(err)
	}
	output := webimage.Output{Data: converted.Bytes(), MediaType: kind, Width: 2, Height: 3, Text: text}
	if version == "1.22.0" {
		output.Width++
	}
	if version == "1.23.0" {
		output.MediaType = "image/png"
	}
	if version == "1.24.0" {
		output.Data = output.Data[:len(output.Data)-20]
	}
	raw, _ := json.Marshal(output)
	plan, _ := json.Marshal(webimage.Plan{Digest: rsiplugin.FileDigest(raw)})
	return rsi.Case{InstanceID: "web-access", Contract: webimage.Contract, Input: json.RawMessage(`{"media_type":"image/png"}`), Inputs: []rsi.InputData{{ID: "image", Data: source.Bytes()}}, Expected: plan, Resources: []rsi.ResourceFixture{{Contract: webimage.OutputContract, Key: "decoded", Input: raw, Output: json.RawMessage(`{}`)}}}
}

func webImageSource(t *testing.T, width, height int) []byte {
	t.Helper()
	pixels := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixels.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x + y), A: 255})
		}
	}
	var source bytes.Buffer
	if err := png.Encode(&source, pixels); err != nil {
		t.Fatal(err)
	}
	return source.Bytes()
}

func TestWebPluginImageActualUpgradeRetainedRunRollbackAndDisable(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	profile := modelInput()
	profile.ContextWindow = 262144
	activateLoop(t, s, profile)
	r.ConfigureWebAccess(cfg, nil)
	policy := webOwnerPolicy(5, 0)
	policy.AllowedContracts = policy.AllowedContracts[:6]
	policy.Cases = policy.Cases[:7]
	h := installRuntimePackage(t, s, r, "web-access", policy, webVersionPackage(t, "0.9.3"))
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := webImageSource(t, 2400, 1200)
	fetches := 0
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, mode string) (webaccess.Page, error) {
		fetches++
		img, text, err := webcontent.ParseImage(c, source, "image/png")
		return webaccess.Page{URL: url, MediaType: "image/png", Image: img, Text: text, RetrievedAt: "2026-11-09T00:00:00Z", Untrusted: true}, err
	}
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			return llm.GenerationResult{Text: "done"}, nil
		}
		return calls(webaccess.ContentName, `{"url":"https://example.com/source"}`), nil
	}
	queue := func(key string) string { return mustSubmit(t, a, key, "Read original image source").RunID }
	check := func(id string, width, height int, kind, text string) {
		t.Helper()
		mustDrain(t, a)
		var raw string
		if err := s.db.QueryRow(`SELECT result_json FROM tool_invocation WHERE run_id=? AND tool_name=?`, id, webaccess.ContentName).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result tool.Result
		var envelope webaccess.ResearchEnvelope
		if json.Unmarshal([]byte(raw), &result) != nil || result.Error != nil || json.Unmarshal([]byte(result.Text), &envelope) != nil || len(result.ImageRefs) != 1 || len(envelope.Pages) != 1 || envelope.Pages[0].Text != text || envelope.Pages[0].URL != "https://example.com/source" || envelope.Pages[0].RetrievedAt != "2026-11-09T00:00:00Z" {
			t.Fatal(raw)
		}
		var mediaType, name string
		if err := s.db.QueryRow(`SELECT media_type,name FROM artifact WHERE artifact_ref=?`, result.ImageRefs[0]).Scan(&mediaType, &name); err != nil {
			t.Fatal(err)
		}
		extension := ".jpg"
		if kind == "image/png" {
			extension = ".png"
		}
		if mediaType != kind || name != "web-image-0"+extension {
			t.Fatal(mediaType, kind, name)
		}
		data := []byte(readArtifact(t, s, toolRunScope(t, s, id), result.ImageRefs[0]))
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width != width || config.Height != height || "image/"+format != kind {
			t.Fatal(config, format, err)
		}
		// The source's quarter-position is RGB(88,44,132). Sampling away from
		// wrapped channel edges tolerates JPEG/resampling variation while
		// rejecting blank/substituted pixels that header checks cannot detect.
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		red, green, blue, _ := decoded.At(width/4, height/4).RGBA()
		for index, pair := range [][2]int{{int(red / 257), 88}, {int(green / 257), 44}, {int(blue / 257), 132}} {
			difference := pair[0] - pair[1]
			if difference < -16 || difference > 16 {
				t.Fatal("source image content lost", index, pair, kind)
			}
		}
	}
	native := queue("image-pre-service")
	base, _ := selectWebVersion(t, h, status.Revision, "1.0.0", 5, 0)
	check(native, 2000, 1000, "image/jpeg", "Image fetched (2000 x 1000)")
	old := queue("image-queued-original")
	small, pkgSmall := selectWebVersion(t, h, base, "1.20.0", 5, 0)
	check(old, 2000, 1000, "image/jpeg", "Image fetched (2000 x 1000)")
	check(queue("image-smaller-new-recipe"), 1200, 600, "image/jpeg", "Evidence image (1200 x 600)")
	retained := queue("image-retained-before-retirement")
	pngRevision, pkgPNG := selectWebVersion(t, h, small, "1.21.0", 5, 0)
	if err = h.Store.Retire(ctx, pkgSmall); err != nil {
		t.Fatal(err)
	}
	check(retained, 1200, 600, "image/jpeg", "Evidence image (1200 x 600)")
	check(queue("image-png-new-recipe"), 2000, 1000, "image/png", "Image fetched (2000 x 1000)")
	activateDelegation(t, h, "image-rollback", pngRevision, base, "web-search-owner-1.0.0")
	if err = h.Store.Retire(ctx, pkgPNG); err != nil {
		t.Fatal(err)
	}
	check(queue("image-rollback-run"), 2000, 1000, "image/jpeg", "Image fetched (2000 x 1000)")
	retained = queue("image-retained-before-disable")
	if _, err = h.Store.Deactivate(ctx, "image-disable", base); err != nil {
		t.Fatal(err)
	}
	check(retained, 2000, 1000, "image/jpeg", "Image fetched (2000 x 1000)")
	before := fetches
	disabled := queue("image-disabled")
	mustDrain(t, a)
	if fetches != before || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE run_id=? AND status='failed'`, disabled) != 1 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal(fetches, before)
	}
}

func TestWebPluginImageCompleteEightMiBSourceAndLargeDerivedOutput(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "large-image", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/full-image"}`}}
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	defer r.Release(ctx, claim)
	defer keepRunClaim(r, claim)()
	pixels := image.NewRGBA(image.Rect(0, 0, 2000, 2000))
	seed := uint32(19421)
	for i := 0; i < len(pixels.Pix); i += 4 {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		pixels.Pix[i], pixels.Pix[i+1], pixels.Pix[i+2], pixels.Pix[i+3] = uint8(seed), uint8(seed>>8), uint8(seed>>16), 255
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, pixels, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if encoded.Len() > 8<<20 {
		t.Fatal(encoded.Len())
	}
	source := make([]byte, 8<<20)
	copy(source, encoded.Bytes())
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		img, text, err := webcontent.ParseImage(c, source, "image/jpeg")
		return webaccess.Page{URL: url, MediaType: "image/jpeg", Image: img, Text: text, Untrusted: true}, err
	}
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error != nil || len(results[0].ImageRefs) != 1 {
		t.Fatal(results, err)
	}
	full := []byte(readArtifact(t, s, toolRunScope(t, s, claim.RunID()), results[0].ImageRefs[0]))
	if len(full) <= 2<<20 {
		t.Fatal("fixture did not cross pure result limit", len(full))
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(full))
	if err != nil || config.Width != 2000 || config.Height != 2000 || format != "jpeg" {
		t.Fatal(config, format, err)
	}
	if scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal("image added a second journal")
	}
}

func TestWebPluginImageAcceptedForgedMetadataCannotPublishOrFallback(t *testing.T) {
	for _, version := range []string{"1.22.0", "1.23.0", "1.24.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{}
			calls := []llm.ToolCall{{ID: "bad-image", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/bad-image"}`}}
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				status, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				selectWebVersion(t, h, status.Revision, version, 5, 0)
			})
			defer r.Release(ctx, claim)
			source := webImageSource(t, 3, 4)
			fetches := 0
			r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
				fetches++
				img, text, err := webcontent.ParseImage(c, source, "image/png")
				return webaccess.Page{URL: url, MediaType: "image/png", Image: img, Text: text, Untrusted: true}, err
			}
			results, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "PROVIDER_UNAVAILABLE" || len(results[0].ImageRefs) != 0 || fetches != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 {
				t.Fatal(results, err, fetches)
			}
			// Original never Tool receipt is authoritative; an invalid derived image is
			// not repaired with native output and cannot cause a second original fetch.
			again, err := executor.Execute(ctx, exchange, calls)
			if err != nil || len(again) != 1 || again[0].Error == nil || fetches != 1 {
				t.Fatal(again, err, fetches)
			}
		})
	}
}

func TestWebPluginImageLateForgetAndGuardianCannotPublishAttachment(t *testing.T) {
	for _, mode := range []string{"forget", "quarantine"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			cfg := webaccess.Config{}
			calls := []llm.ToolCall{{ID: "private-image", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/private-image"}`}}
			var h rsi.Host
			var revision string
			s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
				r.ConfigureWebAccess(cfg, nil)
				h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
				status, err := h.Store.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				revision = status.Revision
			})
			defer r.Release(ctx, claim)
			source := webImageSource(t, 3, 4)
			fetches, reached := 0, 0
			r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
				fetches++
				img, text, err := webcontent.ParseImage(c, source, "image/png")
				return webaccess.Page{URL: url, MediaType: "image/png", Image: img, Text: text, Untrusted: true}, err
			}
			owner := idAgent(t, s)
			s.MainBoundary = func(point string) {
				if point != "after_web_image_method" {
					return
				}
				s.MainBoundary = nil
				reached++
				if mode == "forget" {
					if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
						t.Fatal(err)
					}
				} else if err := h.Store.Quarantine(ctx, revision); err != nil {
					t.Fatal(err)
				}
			}
			results, err := executor.Execute(ctx, exchange, calls)
			if mode == "forget" && !errors.Is(err, agent.ErrClaimLost) {
				t.Fatal(results, err)
			}
			if mode == "quarantine" && err != nil && !errors.Is(err, rsi.ErrRevoked) {
				t.Fatal(results, err)
			}
			if mode == "quarantine" && err == nil && (len(results) != 1 || results[0].Error == nil) {
				t.Fatal(results)
			}
			if reached != 1 || fetches != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM tool_invocation WHERE status='succeeded'`) != 0 {
				t.Fatal(reached, fetches, results, err)
			}
		})
	}
}

func TestWebPluginImageChildUsesRetainedOriginalParserAndPrivateScope(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	s, r, a, p := childFixture(t, webaccess.Definitions(cfg)...)
	r.ConfigureWebAccess(cfg, nil)
	h := installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := h.Store.Graph(ctx, status.Revision)
	if err != nil {
		t.Fatal(err)
	}
	source := webImageSource(t, 2400, 1200)
	fetches := 0
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		fetches++
		img, text, err := webcontent.ParseImage(c, source, "image/png")
		return webaccess.Page{URL: url, MediaType: "image/png", Image: img, Text: text, Untrusted: true}, err
	}
	switched := false
	p.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		last := req.Messages[len(req.Messages)-1]
		if isFollowup(req) {
			return llm.GenerationResult{Text: "Main delivery"}, nil
		}
		if last.Role == "tool" {
			return llm.GenerationResult{Text: "Image task completed"}, nil
		}
		if !isRoot(req) {
			return calls(task.ManageName, `{"operation":"create","task":"PRIVATE_ROOT_IMAGE_INSTRUCTION"}`), nil
		}
		if last.Text == "read child image" {
			return calls(webaccess.ContentName, `{"url":"https://example.com/child-image"}`), nil
		}
		if !switched {
			selectWebVersion(t, h, status.Revision, "1.20.0", 5, 0)
			if err := h.Store.Retire(ctx, graph.Graph.Bindings[0].PackageDigest); err != nil {
				t.Fatal(err)
			}
			switched = true
		}
		return calls(agent.RunToolName, `{"task":"read child image"}`), nil
	}
	mustSubmit(t, a, "image-child-retained", "PRIVATE_MAIN_CONVERSATION")
	mustDrain(t, a)
	if !switched || fetches != 1 || scalar(t, s, `SELECT count(*) FROM run c JOIN rsi_run cb ON cb.run_id=c.run_id JOIN rsi_run pb ON pb.run_id=c.parent_run_id WHERE c.worker_position='child' AND c.status='completed' AND cb.graph_revision=? AND cb.graph_revision=pb.graph_revision AND cb.managed_services_json=pb.managed_services_json AND EXISTS(SELECT 1 FROM json_each(cb.managed_services_json) WHERE value=?)`, status.Revision, webimage.Contract) != 1 {
		t.Fatal(switched, fetches)
	}
	var childID, raw string
	if err = s.db.QueryRow(`SELECT r.run_id,i.result_json FROM tool_invocation i JOIN run r USING(run_id) WHERE r.worker_position='child' AND i.tool_name=? AND i.status='succeeded'`, webaccess.ContentName).Scan(&childID, &raw); err != nil {
		t.Fatal(err)
	}
	var result tool.Result
	if json.Unmarshal([]byte(raw), &result) != nil || len(result.ImageRefs) != 1 || !bytes.Contains([]byte(result.Text), []byte("Image fetched (2000 x 1000)")) {
		t.Fatal(raw)
	}
	data := []byte(readArtifact(t, s, toolRunScope(t, s, childID), result.ImageRefs[0]))
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 2000 || config.Height != 1000 {
		t.Fatal(config, err)
	}
	p.mu.Lock()
	requests := append([]llm.GenerationRequest{}, p.requests...)
	p.mu.Unlock()
	seen := 0
	for _, request := range requests {
		if request.RunID != childID {
			continue
		}
		seen++
		raw, _ := json.Marshal(request.Messages)
		if bytes.Contains(raw, []byte("PRIVATE_ROOT_IMAGE_INSTRUCTION")) || bytes.Contains(raw, []byte("PRIVATE_MAIN_CONVERSATION")) {
			t.Fatal("Child inherited parent private messages")
		}
	}
	if seen == 0 {
		t.Fatal("no actual Child requests")
	}
}

func TestWebPluginImageRealReleaseRecoveryDoesNotReplayUnknownSource(t *testing.T) {
	ctx := context.Background()
	cfg := webaccess.Config{}
	calls := []llm.ToolCall{{ID: "recover-image", Name: webaccess.ContentName, Arguments: `{"url":"https://example.com/recover-image"}`}}
	var h rsi.Host
	s, r, claim, executor, exchange := admitPolicyRun(t, webaccess.Definitions(cfg), calls, false, func(s *Store, r *Runs) {
		r.ConfigureWebAccess(cfg, nil)
		h = installRuntimeFeature(t, s, r, "web-access", webOwnerPolicy(5, 0))
	})
	stop := keepRunClaim(r, claim)
	defer stop()
	status, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source := webImageSource(t, 3, 4)
	fetches, reached := 0, 0
	r.webContentFetch = func(c context.Context, _ webcontent.Config, url, _ string) (webaccess.Page, error) {
		fetches++
		img, text, err := webcontent.ParseImage(c, source, "image/png")
		return webaccess.Page{URL: url, MediaType: "image/png", Image: img, Text: text, Untrusted: true}, err
	}
	s.MainBoundary = func(point string) {
		if point != "after_web_image_method" {
			return
		}
		s.MainBoundary = nil
		reached++
		stop()
		if err := r.Release(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = executor.Execute(ctx, exchange, calls); !errors.Is(err, agent.ErrClaimLost) || reached != 1 || fetches != 1 {
		t.Fatal(reached, fetches, err)
	}
	selectWebVersion(t, h, status.Revision, "1.20.0", 5, 0)
	if next, err := r.Next(ctx); err != nil || next != claim.RunID() {
		t.Fatal("real recovery scan did not recover original run", next, err)
	}
	next, err := r.Claim(ctx, claim.RunID())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release(ctx, next)
	executor.Store = r.ToolStore(next)
	results, err := executor.Execute(ctx, exchange, calls)
	if err != nil || len(results) != 1 || results[0].Error == nil || results[0].Error.Code != "UNKNOWN_SIDE_EFFECT" || fetches != 1 || scalar(t, s, `SELECT count(*) FROM artifact`) != 0 || scalar(t, s, `SELECT count(*) FROM rsi_call`) != 0 {
		t.Fatal(results, fetches, err)
	}
}
