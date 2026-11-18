package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/config"
	"github.com/z-chenhao/assistant/internal/interaction/feishu"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/llm"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	artifactcontract "github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/rsi/builtin"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// This finite test producer changes the actual recipe, not a Host result or a
// stored file. It remains private test setup, not another extension framework.
func artifactHTTPMediaPackage(t *testing.T) string {
	t.Helper()
	// The Linux carrier has no Go compiler. Its owner supplies the exact
	// cross-compiled changed producer; normal Host installation still verifies
	// its complete sealed payload and the independent old/new behavior gates.
	if compiled := os.Getenv("STEWARD_TEST_ARTIFACT_MEDIA_PACKAGE"); compiled != "" {
		raw, err := os.ReadFile(filepath.Join(compiled, "steward.plugin.json"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest rsiplugin.Manifest
		if err := rsiplugin.Decode(raw, &manifest); err != nil || manifest.PluginID != "steward.artifact-workbench" || manifest.Version != "1.2.0" {
			t.Fatal("explicit changed Linux producer required", err)
		}
		return compiled
	}
	directory := t.TempDir()
	replace := map[string]string{}
	for file, change := range map[string][2]string{
		"plugin.go": {`Version: "1.0.0"`, `Version: "1.2.0"`},
		"recipe.go": {`[]string{"image", "audio", "video"}`, `[]string{"audio", "video"}`},
	} {
		path, err := filepath.Abs(filepath.Join("../plugin/artifactworkbench", file))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil || strings.Count(string(raw), change[0]) != 1 {
			t.Fatal("actual media recipe fixture drift", file, err)
		}
		override := filepath.Join(directory, file)
		if err := os.WriteFile(override, []byte(strings.Replace(string(raw), change[0], change[1], 1)), 0600); err != nil {
			t.Fatal(err)
		}
		replace[path] = override
	}
	data, err := json.Marshal(struct{ Replace map[string]string }{replace})
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(directory, "overlay.json")
	if err := os.WriteFile(overlay, data, 0600); err != nil {
		t.Fatal(err)
	}
	packageDir := t.TempDir()
	if out, err := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(packageDir, "plugin"), "../../cmd/plugins/artifact-workbench").CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	data, err = os.ReadFile("../../plugins/artifact-workbench/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.2.0"
	manifest, err = rsi.Seal(packageDir, manifest)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "steward.plugin.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return packageDir
}

func TestArtifactDefaultProductionHTTPChannelsFreezeCreationTypeAcrossVersions(t *testing.T) {
	carrier := os.Getenv("STEWARD_TEST_APP_DEFAULT_BUNDLE")
	if carrier == "" {
		t.Skip("requires actual complete40 distribution;artifact-plugins gate supplies it")
	}
	carrier, err := filepath.Abs(carrier)
	if err != nil {
		t.Fatal(err)
	}
	directories, err := builtin.ShippedDirectories(carrier)
	if err != nil || directories.Artifact == "" {
		t.Fatal("actual complete40 required", err)
	}
	ctx := context.Background()
	root := t.TempDir()
	cfg, err := config.Load("", func(key string) (string, bool) { return root, key == "STEWARD_DATA_ROOT" })
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := filepath.Abs("../../provider-bridge/bridge.mjs")
	if err != nil {
		t.Fatal(err)
	}
	sidecar, err := filepath.Abs("../../runtime/javascript/src/sidecar.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cfg.LLM = config.LLM{ProviderBridgeCommand: node, ProviderBridgeScript: bridge, ProviderBridgeVersion: "pi-ai-1.3.0-v1"}
	cfg.JavaScript = &config.JavaScript{Command: node, Script: sidecar}
	secrets := secretmemory.New()
	var a *App
	defer func() {
		if a != nil {
			_ = a.Close()
		}
	}()
	openApp := func(initial bool) rsi.Host {
		t.Helper()
		if a != nil {
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a = nil
		}
		var err error
		a, err = OpenRuntimeWithSecrets(ctx, cfg, secrets)
		if err != nil {
			t.Fatal(err)
		}
		host := a.Plugins()
		host.Catalog.Launcher = carrier
		a.store.ConfigureArtifactHost(host.Catalog)
		if initial {
			value, err := prepareWebDefault(ctx, host, cfg, directories.Extract)
			if err != nil {
				t.Fatal(err)
			}
			if err := builtin.EnsureDefault(ctx, host, directories, value); err != nil {
				t.Fatal(err)
			}
			// Only the external model probe is a fixture. No model generation is
			// needed to accept the actual authenticated channel intent below.
			auth, err := a.LoginKey(ctx, []byte("synthetic-artifact-model-key"))
			if err != nil {
				t.Fatal(err)
			}
			profile, err := a.Models().AddProfile(ctx, llm.ProfileInput{Name: "Artifact channel transport", ProviderKind: "openai_compatible", WireAPI: "openai_completions", Endpoint: "http://127.0.0.1:1/v1", ModelID: "fixture-model", AuthConnectionID: auth.ConnectionID, ContextWindow: 65536, MaxOutputTokens: 4096, Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true, ToolCalls: true, Streaming: true}})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Models().RecordProbe(ctx, profile, llm.ProbeRecord{ProfileVersion: profile.Version, AuthVersion: auth.Version, BridgeIdentity: a.control.BridgeIdentity, Result: llm.ProbeResult{Streaming: true, ToolCalls: true, Continuation: true, Cancellation: true, Clipping: true}}); err != nil {
				t.Fatal(err)
			}
			diagnostics, err := a.Diagnostics(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.control.Select(ctx, profile.ProfileID, profile.Version, diagnostics.ConfigVersion); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.prepareCore(ctx); err != nil {
			t.Fatal(err)
		}
		a.eventsRequired.Store(false)
		a.runtimeRuns().ConfigurePlugins(host.Catalog)
		return host
	}
	host := openApp(true)
	first, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Store.Graph(ctx, first.Revision)
	if err != nil || len(graph.Graph.Bindings) != 40 {
		t.Fatal("channel used partial composition", err)
	}
	var policyID string
	if err := a.store.Transaction(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT policy_id FROM rsi_candidate WHERE target=?`, first.Revision).Scan(&policyID)
	}); err != nil {
		t.Fatal(err)
	}
	basePolicy, err := host.Store.Policy(ctx, policyID)
	if err != nil || len(basePolicy.Cases) != len(readDefaultOwnerInventory(t).Cases) {
		t.Fatal("full owner cases missing", err)
	}
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.BindChannel(ctx, "web", web.Subject, web.Destination, true, 0); err != nil {
		t.Fatal(err)
	}
	webPost := func(id string, forged bool) int {
		t.Helper()
		var data bytes.Buffer
		form := multipart.NewWriter(&data)
		if err := form.WriteField("source_id", id); err != nil {
			t.Fatal(err)
		}
		if err := form.WriteField("text", "Inspect retained bytes"); err != nil {
			t.Fatal(err)
		}
		header := textproto.MIMEHeader{"Content-Disposition": {`form-data; name="file"; filename="pixel.png"`}, "Content-Type": {"image/png"}}
		file, err := form.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(pixels.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/message", &data)
		r.RemoteAddr = "127.0.0.1:32000"
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("Accept", "application/json")
		if forged {
			r.Header.Set("Origin", "https://untrusted.invalid")
		}
		w := httptest.NewRecorder()
		(&web.Server{Core: a}).ServeHTTP(w, r)
		return w.Code
	}
	var downloads atomic.Int32
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/messages/") || r.Header.Get("Authorization") != "Bearer synthetic-platform-token" {
			http.Error(w, "unexpected transport", http.StatusBadRequest)
			return
		}
		downloads.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pixels.Bytes())
	}))
	defer platform.Close()
	for ref, value := range map[string]string{"credential:artifact-fs-verify": "synthetic-verify", "credential:artifact-fs-encrypt": "synthetic-encrypt", "credential:artifact-fs-access": "synthetic-platform-token"} {
		if err := secrets.Put(ctx, ref, []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	client := feishu.Client{Config: feishu.Config{AppID: "artifact-app", TenantKey: "artifact-tenant", VerificationTokenRef: "credential:artifact-fs-verify", EncryptKeyRef: "credential:artifact-fs-encrypt", AccessTokenRef: "credential:artifact-fs-access", APIBase: platform.URL}, Secrets: secrets}
	if _, err := a.BindChannel(ctx, "feishu", client.Subject("owner"), "chat", true, 0); err != nil {
		t.Fatal(err)
	}
	feishuPost := func(id string, forged bool) int {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]string{"event_id": id, "event_type": "im.message.receive_v1", "app_id": client.Config.AppID, "tenant_key": client.Config.TenantKey, "token": "synthetic-verify"}, "event": map[string]any{"sender": map[string]any{"sender_type": "user", "sender_id": map[string]string{"open_id": "owner"}}, "message": map[string]string{"message_id": id, "chat_id": "chat", "chat_type": "p2p", "message_type": "image", "content": `{"image_key":"media"}`}}})
		if err != nil {
			t.Fatal(err)
		}
		stamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), "artifact-nonce"
		sum := sha256.Sum256(append([]byte(stamp+nonce+"synthetic-encrypt"), raw...))
		r := httptest.NewRequest("POST", "/feishu/callback", bytes.NewReader(raw))
		r.Header.Set("X-Lark-Request-Timestamp", stamp)
		r.Header.Set("X-Lark-Request-Nonce", nonce)
		r.Header.Set("X-Lark-Signature", hex.EncodeToString(sum[:]))
		if forged {
			r.Header.Set("X-Lark-Signature", "forged")
		}
		w := httptest.NewRecorder()
		(feishu.Handler{Client: client, Core: a}).ServeHTTP(w, r)
		return w.Code
	}
	assertSource := func(channel, id, kind, revision string) string {
		t.Helper()
		subject, destination := web.Subject, web.Destination
		if channel == "feishu" {
			subject, destination = client.Subject("owner"), "chat"
		}
		parts, found, err := a.store.ChannelSourceContent(ctx, channel, subject, destination, id)
		if err != nil || !found {
			t.Fatal("actual channel did not accept intent", channel, id, err)
		}
		var reference string
		for _, part := range parts {
			if part.ArtifactRef != "" {
				if reference != "" || part.Type != kind {
					t.Fatal("channel reclassified stored creation from current MIME", parts)
				}
				reference = part.ArtifactRef
			}
		}
		descriptor, err := a.DescribeArtifact(ctx, reference)
		if err != nil || descriptor.SemanticKind != kind || descriptor.MethodGraph != revision || descriptor.MediaType != "image/png" {
			t.Fatal("actual channel lost immutable creation fact", descriptor, err)
		}
		reader, err := a.OpenArtifact(ctx, reference)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(data, pixels.Bytes()) {
			t.Fatal("channel changed original pixels", err)
		}
		return reference
	}
	if webPost("web-old", false) != http.StatusOK || feishuPost("fs-old", false) != http.StatusOK {
		t.Fatal("ordinary real HTTP upload failed")
	}
	oldWeb := assertSource("web", "web-old", "image", first.Revision)
	oldFeishu := assertSource("feishu", "fs-old", "image", first.Revision)
	count := downloads.Load()
	if webPost("forged", true) != http.StatusForbidden || feishuPost("forged", true) != http.StatusUnauthorized || downloads.Load() != count {
		t.Fatal("authentication failure reached file import")
	}
	pkg, err := host.Install(ctx, artifactHTTPMediaPackage(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := range graph.Graph.Bindings {
		if graph.Graph.Bindings[i].InstanceID == "artifact-workbench" {
			graph.Graph.Bindings[i].PackageDigest = pkg.Digest
		}
	}
	updated, err := host.Prepare(ctx, graph.Graph)
	if err != nil {
		t.Fatal(err)
	}
	activate := func(target, id string, policy rsi.Policy) {
		t.Helper()
		policy.ID = id
		status, err := host.Store.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := host.Store.PutPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		if err := host.Propose(ctx, rsi.Candidate{ID: id, Baseline: status.Revision, Target: target, PolicyID: id, Goal: "Independently verify channel creation type"}); err != nil {
			t.Fatal(err)
		}
		decision, err := host.Verify(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := host.Activate(ctx, rsi.Activation{OperationID: id, Expected: status.Revision, Target: target, DecisionID: decision}); err != nil {
			t.Fatal(err)
		}
	}
	// Independent old expectations reject the behavior change before the owner
	// approves a new expectation. No Host/candidate result is used as an oracle.
	bad := basePolicy
	bad.ID = "artifact-http-unapproved"
	if err := host.Store.PutPolicy(ctx, bad); err != nil {
		t.Fatal(err)
	}
	if err := host.Propose(ctx, rsi.Candidate{ID: bad.ID, Baseline: first.Revision, Target: updated.Revision, PolicyID: bad.ID, Goal: "Reject unapproved media change"}); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Verify(ctx, bad.ID); !errors.Is(err, rsi.ErrVerification) {
		t.Fatal("old owner gate accepted changed media semantics", err)
	}
	changed := 0
	for i := range basePolicy.Cases {
		if basePolicy.Cases[i].Contract == artifactcontract.Recipe {
			basePolicy.Cases[i].Expected = json.RawMessage(`{"retention_days":30,"media_kinds":["audio","video"],"overflow_name":"tool-output.txt","overflow_media":"text/plain","stored_text":"Saved artifact.","publish_text":"Final artifacts selected."}`)
			changed++
		}
	}
	if changed != 1 {
		t.Fatal("one independent media example expected", changed)
	}
	activate(updated.Revision, "artifact-http-media-upgrade", basePolicy)
	if webPost("web-new", false) != http.StatusOK || feishuPost("fs-new", false) != http.StatusOK {
		t.Fatal("new media upload failed")
	}
	newWeb := assertSource("web", "web-new", "file", updated.Revision)
	newFeishu := assertSource("feishu", "fs-new", "file", updated.Revision)
	if oldWeb == newWeb || oldFeishu == newFeishu {
		t.Fatal("new input reused original resource identity")
	}
	host = openApp(false)
	if assertSource("web", "web-old", "image", first.Revision) != oldWeb || assertSource("feishu", "fs-new", "file", updated.Revision) != newFeishu {
		t.Fatal("cold restart reinterpreted old input")
	}
	originalPolicy, err := host.Store.Policy(ctx, policyID)
	if err != nil {
		t.Fatal(err)
	}
	activate(first.Revision, "artifact-http-media-rollback", originalPolicy)
	assertSource("web", "web-new", "file", updated.Revision)
	assertSource("feishu", "fs-old", "image", first.Revision)
	if _, err := host.Store.Deactivate(ctx, "artifact-http-disabled", first.Revision); err != nil {
		t.Fatal(err)
	}
	host = openApp(false)
	if webPost("web-new", false) != http.StatusOK || feishuPost("fs-old", false) != http.StatusOK {
		t.Fatal("old accepted input replay needed a current candidate")
	}
	assertSource("web", "web-new", "file", updated.Revision)
	assertSource("feishu", "fs-old", "image", first.Revision)
	if webPost("web-disabled", false) < http.StatusBadRequest || feishuPost("fs-disabled", false) < http.StatusBadRequest {
		t.Fatal("disabled current creation restored native channel policy")
	}
	if err := a.store.Transaction(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM artifact`).Scan(&count); err != nil {
			return err
		}
		if count != 4 {
			return fmt.Errorf("rejected/replayed input created artifacts: %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
