package service

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This peer models only launchctl registration. Real locks, files, heartbeats
// and stop/restart admission remain product code.
type launchPeer struct {
	loaded map[string]bool
	calls  [][]string
	fail   string
}

func (p *launchPeer) command(_ context.Context, args ...string) error {
	p.calls = append(p.calls, append([]string(nil), args...))
	if p.fail == args[0] {
		return errors.New("unavailable")
	}
	switch args[0] {
	case "print":
		if !p.loaded[args[1]] {
			return errors.New("not loaded")
		}
	case "bootstrap":
		label := strings.TrimSuffix(filepath.Base(args[2]), ".plist")
		p.loaded[args[1]+"/"+label] = true
	case "bootout":
		delete(p.loaded, args[1])
	}
	return nil
}
func fixture(t *testing.T) (*Manager, *launchPeer) {
	t.Helper()
	root := t.TempDir()
	m, err := New(filepath.Join(root, "data & root"), filepath.Join(root, "home"), filepath.Join(root, "install & path", "steward"))
	if err != nil {
		t.Fatal(err)
	}
	p := &launchPeer{loaded: map[string]bool{}}
	m.command = p.command
	return m, p
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallIsInactiveAndLaunchArgumentsAreEscaped(t *testing.T) {
	m, p := fixture(t)
	t.Setenv("STEWARD_AUTH_FIXTURE", "PRIVATE_SENTINEL")
	must(t, m.Install(context.Background(), "/private/config & source.yaml"))
	if len(p.calls) != 0 {
		t.Fatal("installation enabled a job", p.calls)
	}
	v, err := m.Status()
	must(t, err)
	if !v.Installed || v.Enabled || v.Running || v.Code != "stopped" {
		t.Fatal(v)
	}
	for _, inspect := range []bool{false, true} {
		raw, err := os.ReadFile(m.plistPath(inspect))
		must(t, err)
		if strings.Contains(string(raw), "PRIVATE_SENTINEL") || strings.Contains(string(raw), "STEWARD_AUTH_FIXTURE") || strings.Contains(string(raw), "KeepAlive") {
			t.Fatal("secret or unconditional restart in launch agent")
		}
		d := xml.NewDecoder(strings.NewReader(string(raw)))
		var values []string
		for {
			token, err := d.Token()
			if err == io.EOF {
				break
			}
			must(t, err)
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "string" {
				var value string
				must(t, d.DecodeElement(&value, &start))
				values = append(values, value)
			}
		}
		operation := "run"
		if inspect {
			operation = "inspect"
		}
		if len(values) < 5 || !reflect.DeepEqual(values[1:5], []string{m.executable, "service", operation, m.root}) {
			t.Fatal("argv changed", values)
		}
		if inspect != strings.Contains(string(raw), "<key>StartInterval</key><integer>60</integer>") {
			t.Fatal("inspector not independent interval job")
		}
		info, err := os.Stat(m.plistPath(inspect))
		must(t, err)
		if info.Mode().Perm() != 0600 {
			t.Fatal("permissive launch agent", info.Mode())
		}
	}
}

func TestStopIntentSurvivesFailedUnloadRestartAndInspection(t *testing.T) {
	m, p := fixture(t)
	ctx := context.Background()
	must(t, m.Install(ctx, ""))
	must(t, m.Start(ctx))
	session, _, err := beginOriginalRecipe(t, m)
	must(t, err)
	must(t, session.Heartbeat("ready"))
	if _, _, err := beginOriginalRecipe(t, m); !errors.Is(err, ErrBusy) {
		t.Fatal("second runtime admitted", err)
	}
	if err := m.Install(ctx, ""); err == nil {
		t.Fatal("upgraded live deployment")
	}
	p.fail = "bootout"
	if err := m.Stop(ctx); err == nil {
		t.Fatal("failed unload hidden")
	}
	enabled, err := session.Enabled()
	must(t, err)
	if enabled {
		t.Fatal("failed signaling erased human stop")
	}
	must(t, session.Close())
	p.fail = ""
	before := len(p.calls)
	for i := 0; i < 3; i++ {
		v, err := m.Inspect(ctx)
		must(t, err)
		if v.Enabled || v.Code != "stopped" {
			t.Fatal(v)
		}
	}
	if len(p.calls) != before {
		t.Fatal("stopped inspector touched launchctl")
	}
	if _, _, err := beginOriginalRecipe(t, m); !errors.Is(err, ErrStopped) {
		t.Fatal("login restored stopped intent", err)
	}
	// A new manager/process sees exactly the same stopped authority.
	m2, err := New(m.root, m.home, m.executable)
	must(t, err)
	v, err := m2.Status()
	must(t, err)
	if v.Enabled || v.Code != "stopped" {
		t.Fatal(v)
	}
	must(t, m.Start(ctx))
	session, _, err = beginOriginalRecipe(t, m)
	must(t, err)
	must(t, session.Close())
}

func TestInspectorOnlyRestartsDeadProcessesAndLimitsCrashLoops(t *testing.T) {
	m, p := fixture(t)
	ctx := context.Background()
	now := time.Now()
	m.now = func() time.Time { return now }
	must(t, m.Install(ctx, ""))
	must(t, m.Start(ctx))
	session, _, err := beginOriginalRecipe(t, m)
	must(t, err)
	for _, code := range []string{"setup_required", "credential_or_provider_unavailable", "dependency_unavailable", "health_unavailable"} {
		must(t, session.Heartbeat(code))
		before := len(p.calls)
		v, err := m.Inspect(ctx)
		must(t, err)
		if v.Code != code || len(p.calls) != before {
			t.Fatal("live degradation restarted", v, p.calls[before:])
		}
		sequence := v.NoticeSequence
		v, err = m.Inspect(ctx)
		must(t, err)
		if v.NoticeSequence != sequence {
			t.Fatal("repeated notice", v)
		}
	}
	now = now.Add(time.Minute)
	v, err := m.Inspect(ctx)
	must(t, err)
	if v.Code != "health_stale" {
		t.Fatal("stale health looked healthy", v)
	}
	must(t, session.Close()) // kernel releases the lifetime lock on process death.
	for i := 0; i < 3; i++ {
		now = now.Add(time.Minute)
		before := len(p.calls)
		v, err = m.Inspect(ctx)
		must(t, err)
		if v.Code != "process_exited" || len(p.calls) == before {
			t.Fatal("crash not restarted", v)
		}
		before = len(p.calls)
		_, err = m.Inspect(ctx)
		must(t, err)
		if len(p.calls) != before {
			t.Fatal("restart not rate limited")
		}
	}
	now = now.Add(time.Minute)
	before := len(p.calls)
	v, err = m.Inspect(ctx)
	must(t, err)
	if v.Code != "restart_limit" || len(p.calls) != before {
		t.Fatal("crash loop not stopped", v)
	}
	v, err = m.Status()
	must(t, err)
	if v.Code != "restart_limit" {
		t.Fatal("operator cannot see restart exhaustion", v)
	}
	// Explicit start resets the crash budget even if enabled remains true.
	must(t, m.Start(ctx))
	session, _, err = beginOriginalRecipe(t, m)
	must(t, err)
	must(t, session.Heartbeat("ready"))
	v, err = m.Inspect(ctx)
	must(t, err)
	if v.Code != "ready" {
		t.Fatal(v)
	}
	must(t, session.Close())
}

func TestFatalSetupRequiresExplicitRepairAndUpgradePreservesStop(t *testing.T) {
	m, p := fixture(t)
	ctx := context.Background()
	must(t, m.Install(ctx, "/old/config"))
	must(t, m.Start(ctx))
	session, path, err := beginOriginalRecipe(t, m)
	must(t, err)
	if path != "/old/config" {
		t.Fatal(path)
	}
	must(t, session.Heartbeat("configuration_error"))
	must(t, session.Close())
	before := len(p.calls)
	v, err := m.Inspect(ctx)
	must(t, err)
	if v.Code != "configuration_error" || len(p.calls) != before {
		t.Fatal("fatal config restarted", v)
	}
	must(t, m.Stop(ctx))
	must(t, m.Install(ctx, "/new/config"))
	v, err = m.Status()
	must(t, err)
	if v.Enabled || v.Code != "stopped" {
		t.Fatal("upgrade enabled execution", v)
	}
	must(t, m.Start(ctx))
	session, path, err = beginOriginalRecipe(t, m)
	must(t, err)
	if path != "/new/config" {
		t.Fatal(path)
	}
	must(t, session.Close())
	must(t, m.Uninstall(ctx))
	v, err = m.Status()
	must(t, err)
	if v.Installed || v.Enabled || v.Code != "not_installed" {
		t.Fatal(v)
	}
}

func TestMetadataAndControlLocksFailClosed(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	must(t, m.Install(ctx, ""))
	lock, err := m.lock("control.lock")
	must(t, err)
	if err := m.Start(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if _, err := m.Inspect(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	must(t, lock.Close())
	must(t, os.WriteFile(filepath.Join(m.dir(), "settings.json"), []byte(`{"version":999,"enabled":true}`), 0600))
	if err := m.Start(ctx); err == nil {
		t.Fatal("incompatible metadata started service")
	}
	if _, _, err := beginOriginalRecipe(t, m); err == nil {
		t.Fatal("incompatible metadata admitted runtime")
	}
	other, _ := fixture(t)
	must(t, os.MkdirAll(filepath.Dir(other.plistPath(false)), 0700))
	must(t, os.WriteFile(other.plistPath(false), []byte("unmanaged"), 0600))
	if err := other.Install(ctx, ""); err == nil {
		t.Fatal("unmanaged launch agent overwritten")
	}
}

func TestInactiveInstallCanFinishInterruptedJobPublication(t *testing.T) {
	m, p := fixture(t)
	ctx := context.Background()
	must(t, m.Install(ctx, ""))
	must(t, os.Remove(m.plistPath(true)))
	// Model process death: discard the manager and recover ownership only from
	// the durable inactive intent, with the second job still absent.
	reopened, err := New(m.root, m.home, m.executable)
	must(t, err)
	reopened.command = p.command
	before := len(p.calls)
	must(t, reopened.Install(ctx, "/repaired/config"))
	if _, err := os.Stat(m.plistPath(true)); err != nil {
		t.Fatal(err)
	}
	for _, call := range p.calls[before:] {
		if call[0] == "bootstrap" || call[0] == "kickstart" {
			t.Fatal("repair enabled execution", call)
		}
	}
	v, err := m.Status()
	must(t, err)
	if v.Enabled || v.Code != "stopped" {
		t.Fatal(v)
	}
	// Retiring an interrupted installation is also recoverable after restart.
	must(t, os.Remove(m.plistPath(true)))
	reopened, err = New(m.root, m.home, m.executable)
	must(t, err)
	reopened.command = p.command
	must(t, reopened.Uninstall(ctx))
	if _, err := os.Stat(m.plistPath(false)); !os.IsNotExist(err) {
		t.Fatal("partial job retained", err)
	}
	v, err = reopened.Status()
	must(t, err)
	if v.Installed || v.Enabled {
		t.Fatal(v)
	}
}

// Mirrors real background startup: freeze the independently captured original
// recipe once, before any heartbeat/crash. Retained recovery never re-admits it.
func beginOriginalRecipe(t *testing.T, m *Manager) (*Session, string, error) {
	t.Helper()
	session, path, err := m.Begin()
	if err == nil && session.NeedsRestartRecipe() {
		receipt := RestartReceipt{GraphRevision: "sha256:" + strings.Repeat("a", 64), Recipe: RestartRecipe{Attempts: 3, RetryDelayMS: 60000}}
		if err = session.FreezeRestartRecipe(&receipt); err != nil {
			session.Close()
			return nil, "", err
		}
	}
	return session, path, err
}
