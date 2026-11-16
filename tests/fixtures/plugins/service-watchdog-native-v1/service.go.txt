// Package service owns the private macOS user-service deployment. It never
// changes Task intent, retries effects, or opens application databases.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

var ErrStopped = errors.New("background service is not enabled")
var ErrBusy = errors.New("background service operation is busy")

// Manager uses one private deployment per canonical data root. Production
// callers use launchctl; tests replace only that external protocol peer.
type Manager struct {
	root, home, executable string
	command                func(context.Context, ...string) error
	now                    func() time.Time
}

type settings struct {
	Version        int    `json:"version"`
	Installed      bool   `json:"installed"`
	Enabled        bool   `json:"enabled"`
	ConfigPath     string `json:"config_path,omitempty"`
	LastRestart    int64  `json:"last_restart,omitempty"`
	Restarts       int    `json:"restarts"`
	Notice         string `json:"notice"`
	NoticeSequence int64  `json:"notice_sequence"`
}

type heartbeat struct {
	Code string `json:"code"`
	At   int64  `json:"at"`
}

// Status exposes fixed diagnostic codes, never configuration or provider text.
type Status struct {
	Installed      bool   `json:"installed"`
	Enabled        bool   `json:"enabled"`
	Running        bool   `json:"running"`
	Code           string `json:"code"`
	Notice         string `json:"notice,omitempty"`
	NoticeSequence int64  `json:"notice_sequence"`
	LastCheckedAt  string `json:"last_checked_at,omitempty"`
}

func New(root, home, executable string) (*Manager, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(home) || !filepath.IsAbs(executable) {
		return nil, errors.New("service paths must be absolute")
	}
	// Resolve existing aliases so the same data root always has one job label.
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, errors.New("create service data root")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, errors.New("resolve service data root")
	}
	m := &Manager{root: root, home: home, executable: executable, now: time.Now}
	m.command = func(ctx context.Context, args ...string) error {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "/bin/launchctl", args...).Run(); err != nil {
			return errors.New("launchctl operation failed; inspect the user login session and service status")
		}
		return nil
	}
	return m, nil
}

func (m *Manager) dir() string { return filepath.Join(m.root, ".service") }
func (m *Manager) label() string {
	hash := sha256.Sum256([]byte(m.root))
	return fmt.Sprintf("com.steward.service.%x", hash[:12])
}
func (m *Manager) domain() string { return "gui/" + strconv.Itoa(os.Getuid()) }
func (m *Manager) target(inspector bool) string {
	label := m.label()
	if inspector {
		label += ".inspect"
	}
	return m.domain() + "/" + label
}
func (m *Manager) plistPath(inspector bool) string {
	label := m.label()
	if inspector {
		label += ".inspect"
	}
	return filepath.Join(m.home, "Library", "LaunchAgents", label+".plist")
}

func (m *Manager) lock(name string) (*os.File, error) {
	if err := os.MkdirAll(m.dir(), 0700); err != nil {
		return nil, errors.New("create private service directory")
	}
	info, err := os.Lstat(m.dir())
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("service directory must be a real directory")
	}
	fd, err := unix.Open(filepath.Join(m.dir(), name), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("open service lock")
	}
	f := os.NewFile(uintptr(fd), name)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrBusy
	}
	return f, nil
}

func readJSON(path string, v any) error {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
		return errors.New("invalid service metadata")
	}
	d := json.NewDecoder(io.LimitReader(f, 16385))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("invalid service metadata")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("invalid service metadata")
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicFile(path, append(b, '\n'))
}

func atomicFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".steward-service-")
	if err != nil {
		return errors.New("create service metadata")
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return errors.New("write service metadata")
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return errors.New("publish service metadata")
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (m *Manager) load() (settings, error) {
	var s settings
	err := readJSON(filepath.Join(m.dir(), "settings.json"), &s)
	if os.IsNotExist(err) {
		return settings{Version: 1}, nil
	}
	if err != nil || s.Version != 1 || s.Restarts < 0 || s.NoticeSequence < 0 || s.LastRestart < 0 || s.ConfigPath != "" && !filepath.IsAbs(s.ConfigPath) {
		return s, errors.New("invalid or incompatible service settings")
	}
	return s, nil
}
func (m *Manager) save(s settings) error {
	return writeJSON(filepath.Join(m.dir(), "settings.json"), s)
}
func (m *Manager) notify(s *settings, code string) {
	if s.Notice != code {
		s.Notice = code
		s.NoticeSequence++
	}
}

func (m *Manager) running() (bool, error) {
	f, err := m.lock("run.lock")
	if errors.Is(err, ErrBusy) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	f.Close()
	return false, nil
}

// Install writes inactive launchd jobs. Only Start grants ongoing execution.
func (m *Manager) Install(ctx context.Context, configPath string) error {
	lock, err := m.lock("control.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	s, err := m.load()
	if err != nil {
		return err
	}
	running, err := m.running()
	if err != nil {
		return err
	}
	if running || s.Enabled {
		return errors.New("stop the service before installing or upgrading")
	}
	if configPath != "" && !filepath.IsAbs(configPath) {
		return errors.New("service configuration path must be absolute")
	}
	// Refuse an unrelated existing job instead of silently claiming it.
	for _, inspector := range []bool{false, true} {
		if _, err := os.Lstat(m.plistPath(inspector)); err == nil && !s.Installed {
			return errors.New("refuse to overwrite an unmanaged launch agent")
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(m.plistPath(false)), 0700); err != nil {
		return err
	}
	wasInstalled := s.Installed
	s.Installed, s.ConfigPath = true, configPath
	m.notify(&s, "stopped")
	// Record ownership before publishing either job. If installation is killed
	// between files, the next inactive Install can safely finish the same jobs.
	if err := m.save(s); err != nil {
		return err
	}
	for _, inspector := range []bool{false, true} {
		// Unload an old stopped deployment before replacing its executable path.
		if wasInstalled {
			if err := m.unload(ctx, inspector); err != nil {
				return err
			}
		}
		if err := atomicFile(m.plistPath(inspector), m.plist(inspector)); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ensureLoaded(ctx context.Context, inspector bool) error {
	if m.command(ctx, "print", m.target(inspector)) == nil {
		return nil
	}
	return m.command(ctx, "bootstrap", m.domain(), m.plistPath(inspector))
}
func (m *Manager) unload(ctx context.Context, inspector bool) error {
	if m.command(ctx, "print", m.target(inspector)) != nil {
		return nil
	}
	return m.command(ctx, "bootout", m.target(inspector))
}

func (m *Manager) Start(ctx context.Context) error {
	lock, err := m.lock("control.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	s, err := m.load()
	if err != nil {
		return err
	}
	if !s.Installed {
		return errors.New("install the background service first")
	}
	if s.Enabled {
		running, err := m.running()
		if err != nil {
			return err
		}
		if running {
			return nil
		}
	}
	s.Enabled, s.Restarts, s.LastRestart = true, 0, 0
	m.notify(&s, "starting")
	// Clear prior fatal health before accepting a new explicit start.
	if err := writeJSON(filepath.Join(m.dir(), "heartbeat.json"), heartbeat{Code: "starting", At: m.now().UnixMicro()}); err != nil {
		return err
	}
	if err := m.save(s); err != nil {
		return err
	}
	for _, inspector := range []bool{true, false} {
		if err := m.ensureLoaded(ctx, inspector); err != nil {
			s.Enabled = false
			m.notify(&s, "manager_unavailable")
			return errors.Join(err, m.save(s))
		}
	}
	// bootstrap may have run the job already; kickstart without -k never kills
	// an active process. The run lock is a second, independent admission fence.
	return m.command(ctx, "kickstart", m.target(false))
}

func (m *Manager) Stop(ctx context.Context) error {
	lock, err := m.lock("control.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	s, err := m.load()
	if err != nil {
		return err
	}
	s.Enabled = false
	m.notify(&s, "stopped")
	// Persist intent before signaling either job. A failed bootout or a reboot
	// cannot make a queued inspector resurrect manually stopped work.
	if err := m.save(s); err != nil {
		return err
	}
	return errors.Join(m.unload(ctx, false), m.unload(ctx, true))
}

func (m *Manager) Uninstall(ctx context.Context) error {
	if err := m.Stop(ctx); err != nil {
		return err
	}
	lock, err := m.lock("control.lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	s, err := m.load()
	if err != nil {
		return err
	}
	if s.Enabled {
		return ErrBusy
	}
	if !s.Installed {
		return nil
	}
	for _, inspector := range []bool{false, true} {
		if err := os.Remove(m.plistPath(inspector)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	s.Installed = false
	return m.save(s)
}

func (m *Manager) status(s settings) (Status, error) {
	v := Status{Installed: s.Installed, Enabled: s.Enabled, Notice: s.Notice, NoticeSequence: s.NoticeSequence}
	var err error
	v.Running, err = m.running()
	if err != nil {
		return v, err
	}
	switch {
	case !s.Installed:
		v.Code = "not_installed"
	case !s.Enabled:
		v.Code = "stopping"
		if !v.Running {
			v.Code = "stopped"
		}
	case !v.Running:
		v.Code = "process_exited"
		var h heartbeat
		if readJSON(filepath.Join(m.dir(), "heartbeat.json"), &h) == nil && (h.Code == "configuration_error" || h.Code == "startup_failed") {
			v.Code = h.Code
		}
		if s.Notice == "restart_limit" {
			v.Code = "restart_limit"
		}
	default:
		var h heartbeat
		if readJSON(filepath.Join(m.dir(), "heartbeat.json"), &h) != nil || h.At <= 0 || !healthCode(h.Code) {
			v.Code = "health_unavailable"
		} else {
			v.Code = h.Code
			v.LastCheckedAt = time.UnixMicro(h.At).UTC().Format(time.RFC3339Nano)
			age := m.now().Sub(time.UnixMicro(h.At))
			if age > 30*time.Second || age < -30*time.Second {
				v.Code = "health_stale"
			}
		}
	}
	return v, nil
}

func (m *Manager) Status() (Status, error) {
	lock, err := m.lock("control.lock")
	if err != nil {
		return Status{}, err
	}
	defer lock.Close()
	s, err := m.load()
	if err != nil {
		return Status{}, err
	}
	return m.status(s)
}

// Inspect runs as a separate launchd interval job. It never drives an Actor,
// opens domain state, or retries an external action. Only process death permits
// restart; a live degraded/stale process is left for explicit operator repair.
func (m *Manager) Inspect(ctx context.Context) (Status, error) {
	lock, err := m.lock("control.lock")
	if err != nil {
		return Status{}, err
	}
	defer lock.Close()
	s, err := m.load()
	if err != nil {
		return Status{}, err
	}
	v, err := m.status(s)
	if err != nil {
		return v, err
	}
	if !s.Enabled || !s.Installed {
		return v, nil
	}
	code := v.Code
	if !v.Running {
		var h heartbeat
		_ = readJSON(filepath.Join(m.dir(), "heartbeat.json"), &h)
		if h.Code == "configuration_error" || h.Code == "startup_failed" {
			code = h.Code
		} else if s.Restarts >= 3 {
			code = "restart_limit"
		} else if m.now().Sub(time.UnixMicro(s.LastRestart)) >= time.Minute {
			s.Restarts++
			s.LastRestart = m.now().UnixMicro()
			// Persist retry admission before dispatch, so inspector crashes cannot
			// bypass throttling. An ambiguous kickstart is never repeated at once.
			m.notify(&s, "process_exited")
			if err := m.save(s); err != nil {
				return v, err
			}
			if err := m.ensureLoaded(ctx, false); err != nil {
				return v, err
			}
			if err := m.command(ctx, "kickstart", m.target(false)); err != nil {
				return v, err
			}
			code = "process_exited"
		}
	} else if v.Code == "ready" {
		s.Restarts = 0
	}
	m.notify(&s, code)
	if err := m.save(s); err != nil {
		return v, err
	}
	v.Notice, v.NoticeSequence = s.Notice, s.NoticeSequence
	v.Code = code
	return v, nil
}

type Session struct {
	manager *Manager
	lock    *os.File
}

// Begin checks persistent human intent and holds the OS lifetime lock before
// application startup. Process death releases the fence automatically.
func (m *Manager) Begin() (*Session, string, error) {
	control, err := m.lock("control.lock")
	if err != nil {
		return nil, "", err
	}
	defer control.Close()
	s, err := m.load()
	if err != nil {
		return nil, "", err
	}
	if !s.Installed || !s.Enabled {
		return nil, "", ErrStopped
	}
	lock, err := m.lock("run.lock")
	if err != nil {
		return nil, "", err
	}
	session := &Session{manager: m, lock: lock}
	if err := session.Heartbeat("starting"); err != nil {
		lock.Close()
		return nil, "", err
	}
	return session, s.ConfigPath, nil
}

func (s *Session) Heartbeat(code string) error {
	if !healthCode(code) {
		return errors.New("invalid service health code")
	}
	return writeJSON(filepath.Join(s.manager.dir(), "heartbeat.json"), heartbeat{Code: code, At: s.manager.now().UnixMicro()})
}

func healthCode(code string) bool {
	switch code {
	case "starting", "ready", "setup_required", "credential_or_provider_unavailable", "dependency_unavailable", "health_unavailable", "configuration_error", "startup_failed", "process_exited":
		return true
	default:
		return false
	}
}
func (s *Session) Enabled() (bool, error) {
	settings, err := s.manager.load()
	return settings.Installed && settings.Enabled, err
}
func (s *Session) Close() error { return s.lock.Close() }
