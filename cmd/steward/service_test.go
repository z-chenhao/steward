package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServiceCommandGrammarAndUnsupportedPlatform(t *testing.T) {
	for _, args := range [][]string{{"service", "install"}, {"service", "start"}, {"service", "stop", "/private/data"}, {"service", "run", "/private/data"}, {"service", "alerts-feishu", "/private/data", "oc_chat"}, {"service", "alerts-off", "/private/data"}} {
		o, err := parseOptions(args, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		c, err := selectCommand(o.flags, strings.NewReader(""))
		if err != nil || c.kind != commandService {
			t.Fatal(c, err)
		}
	}
	if runtime.GOOS != "darwin" {
		root := filepath.Join(t.TempDir(), "untouched")
		t.Setenv("STEWARD_DATA_ROOT", root)
		if err := run(context.Background(), []string{"service", "install"}, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "macOS") {
			t.Fatal(err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("unsupported platform opened state", err)
		}
	}
}

func TestBackgroundCLIExposesSetupAndObservesPersistentStop(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS CLI; portable service protocol and lock coverage runs separately")
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	root := filepath.Join(dir, "data")
	t.Setenv("HOME", home)
	t.Setenv("STEWARD_DATA_ROOT", root)
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("version: 1\nweb:\n  listen: 127.0.0.1:0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-config", cfg, "service", "install"}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "state.db")); !os.IsNotExist(err) {
		t.Fatal("inactive install opened domain state")
	}
	// Seed enabled deployment intent as a process-boundary fixture. The complete
	// Start/launchctl protocol is independently tested against a registration peer;
	// this test never bootstraps an agent into the developer's login session.
	settingsPath := filepath.Join(root, ".service", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	settings["enabled"] = true
	write := func() {
		t.Helper()
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		temp := settingsPath + ".test"
		if err := os.WriteFile(temp, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, settingsPath); err != nil {
			t.Fatal(err)
		}
	}
	write()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() {
		err := run(ctx, []string{"service", "run", root}, strings.NewReader(""), writer, io.Discard)
		writer.CloseWithError(err)
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("service did not join")
		}
	})
	var address struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(reader).Decode(&address); err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 3 * time.Second}
	for i := 0; i < 2; i++ {
		response, err := client.Get(address.URL + "/api/status")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || !strings.Contains(string(body), "setup_required") {
			t.Fatal(response.StatusCode, string(body))
		}
	}
	// Closing a browser connection does not own/cancel the background runtime.
	out.Reset()
	if err := run(context.Background(), []string{"service", "status", root}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"running":true`) {
		t.Fatal(out.String())
	}
	settings["enabled"] = false
	write()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(10 * time.Second):
		t.Fatal("service ignored persistent human stop")
	}
	out.Reset()
	if err := run(context.Background(), []string{"service", "inspect", root}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"code":"stopped"`) || strings.Contains(out.String(), `"enabled":true`) {
		t.Fatal(out.String())
	}
	// A queued launch after stop returns without reopening domain state.
	if err := run(context.Background(), []string{"service", "run", root}, strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestServiceAlertCLIPersistsSelectionAndDisablesWithoutRuntime(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("service CLI is macOS only")
	}
	home := t.TempDir()
	root := filepath.Join(home, "data")
	t.Setenv("HOME", home)
	t.Setenv("STEWARD_DATA_ROOT", root)
	cfg := filepath.Join(home, "service.yaml")
	raw := []byte("version: 1\nfeishu:\n  app_id: fixture\n  tenant_key: fixture\n  verification_token_ref: 'credential:verify'\n  encrypt_key_ref: 'credential:encrypt'\n  access_token_ref: 'credential:watchdog'\n")
	if err := os.WriteFile(cfg, raw, 0600); err != nil {
		t.Fatal(err)
	}
	execute := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := run(context.Background(), args, strings.NewReader(""), &out, io.Discard); err != nil {
			t.Fatal(args, err)
		}
		return out.String()
	}
	execute("-config", cfg, "service", "install")
	out := execute("service", "alerts-feishu", root, "oc_selected")
	if !strings.Contains(out, `"policy":"waiting_for_watchdog_recipe"`) || strings.Contains(out, "oc_selected") {
		t.Fatal(out)
	}
	if err := os.WriteFile(cfg, []byte("invalid: configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := execute("service", "status", root); !strings.Contains(got, `"enabled":true`) {
		t.Fatal(got)
	}
	if got := execute("service", "alerts-off", root); strings.Contains(got, `"enabled":true`) {
		t.Fatal(got)
	}
	state, err := os.ReadFile(filepath.Join(root, ".service", "alerts.json"))
	if err != nil || strings.Contains(string(state), "oc_selected") {
		t.Fatal(string(state), err)
	}
}
