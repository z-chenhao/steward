package macosvm

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/llm"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

const defaultImage = "macos-tahoe-cua:26.5.2"
const defaultImageDigest = "ed1783e80e08e888889b54a8c0387105a37fbcfc995079f76db48e2280081487"
const legacyPreparationStopped = "Desktop preparation stopped. Check free disk space and network access, then restart Steward to retry."

func DefaultConfig(root string) Config {
	return Config{Managed: true, Root: root, Compatibility: computer.Compatibility{ProviderVersion: "0.6.1", GuestBuild: "25F84", DriverVersion: "0.36.0", DriverSHA256: "54bb23df35ae9a52b6fa7b71e607364d0711bbbf750b9edb006e87b9af586fdd", DriverTeam: "YCK386LBJ7", DriverBundle: "com.trycua.driver", Architecture: "arm64"}, Manifest: computer.RestorationManifest{Version: 1}}
}

type managedRuntime struct {
	mu         sync.Mutex
	root       string
	secrets    llm.SecretStore
	cancel     context.CancelFunc
	lifetime   context.Context
	done       chan struct{}
	err        error
	ready      bool
	server     *exec.Cmd
	serverDone chan error
	lock       *os.File
	locked     bool
}

// ManagedState is a read-only settings projection; opening Settings never
// creates directories, downloads an image, or starts a process.
func ManagedState(root string) (string, string) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return "unsupported", "Automatic macOS desktops require an Apple silicon Mac. Local files and commands remain available."
	}
	var s struct {
		State   string `json:"state"`
		Message string `json:"message"`
	}
	if readJSON(filepath.Join(root, "status.json"), &s) == nil {
		if s.State == "unavailable" && s.Message == legacyPreparationStopped {
			s.Message = "The previous desktop preparation failed. Restart Steward, then use Computer to retry."
		}
		return s.State, s.Message
	}
	return "automatic", "The private macOS desktop will download and start on first use."
}

func newManaged(c Config, guest Guest) (*Provider, error) {
	g, ok := guest.(SSHGuest)
	if c.Validate() != nil || !ok || g.Secrets == nil {
		return nil, computer.ErrInvalid
	}
	p := &Provider{config: c, guest: guest, verified: map[string]verifiedUnit{}}
	p.managed = &managedRuntime{root: filepath.Dir(c.Root), secrets: g.Secrets}
	return p, nil
}

// Prepare starts shared installation outside the invocation timeout. The
// accepted call waits on the existing resource scheduler, without a VM lease.
func (p *Provider) Prepare(ctx context.Context) error {
	m := p.managed
	if m == nil {
		return nil
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return computer.ErrUnavailable
	}
	return m.startPreparation(ctx, p.prepareManaged)
}

func (m *managedRuntime) startPreparation(ctx context.Context, prepare func(context.Context) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready {
		return nil
	}
	if m.done != nil {
		select {
		case <-m.done:
			return m.err
		default:
			return computer.ErrPreparing
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	lifetime, cancel := context.WithCancel(context.Background())
	work, stop := context.WithTimeout(lifetime, 2*time.Hour)
	m.lifetime = lifetime
	m.cancel = cancel
	m.done = make(chan struct{})
	go func() {
		defer stop()
		err := prepare(work)
		if err != nil {
			_ = m.stopServer()
			state, message := preparationFailure(err)
			m.status(state, message)
		}
		m.mu.Lock()
		m.err = err
		m.ready = err == nil
		close(m.done)
		m.mu.Unlock()
	}()
	return computer.ErrPreparing
}

func preparationFailure(err error) (string, string) {
	switch {
	case errors.Is(err, context.Canceled):
		return "automatic", "Desktop preparation was interrupted. Use Computer after restarting Steward to resume."
	case errors.Is(err, context.DeadlineExceeded):
		return "unavailable", "Desktop preparation timed out. Restart Steward, then use Computer to retry."
	case errors.Is(err, computer.ErrCompatibility):
		return "unavailable", "The desktop runtime or image does not match this Steward version."
	case errors.Is(err, computer.ErrIntegrity):
		return "unavailable", "Desktop verification failed. Restart Steward, then use Computer to retry."
	case errors.Is(err, unix.ENOSPC):
		return "unavailable", "There is not enough disk space to prepare the desktop. Free space, restart Steward, then use Computer to retry."
	case errors.Is(err, os.ErrPermission):
		return "unavailable", "Desktop preparation could not access its private files. Check permissions, restart Steward, then use Computer to retry."
	default:
		return "unavailable", "Desktop preparation failed. Restart Steward, then use Computer to retry."
	}
}

func (m *managedRuntime) close() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	_ = m.stopServer()
	if m.lock != nil {
		_ = m.lock.Close()
	}
}

func (m *managedRuntime) status(state, message string) {
	if !m.locked || privateRoot(m.root) != nil {
		return
	}
	raw, _ := json.Marshal(map[string]string{"state": state, "message": message})
	tmp := filepath.Join(m.root, ".status.json")
	if os.WriteFile(tmp, raw, 0600) == nil {
		_ = os.Rename(tmp, filepath.Join(m.root, "status.json"))
	}
}

func (m *managedRuntime) command(ctx context.Context, args ...string) *exec.Cmd {
	binary := filepath.Join(m.root, "runtime", "lume.app", "Contents", "MacOS", "lume")
	cmd := exec.CommandContext(ctx, binary, args...)
	// Lume's config and caches belong only to this installation. It never opens
	// the user's ~/.lume registry, SSH agent or provider credentials.
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "XDG_CONFIG_HOME=" + filepath.Join(m.root, "config"), "LUME_TELEMETRY_ENABLED=false"}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return cmd
}

func (p *Provider) prepareManaged(ctx context.Context) error {
	m := p.managed
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return err
	}
	if privateRoot(m.root) != nil {
		return computer.ErrIntegrity
	}
	fd, err := unix.Open(filepath.Join(m.root, "managed.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	m.lock = os.NewFile(uintptr(fd), "managed desktop")
	for unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		if err := pause(ctx); err != nil {
			return err
		}
	}
	m.locked = true
	for _, name := range []string{"runtime", "config", "config/lume", "cache", "seed", "seed/vms"} {
		if err = os.MkdirAll(filepath.Join(m.root, name), 0700); err != nil {
			return err
		}
		if privateRoot(filepath.Join(m.root, name)) != nil {
			return computer.ErrIntegrity
		}
	}
	m.status("preparing", "Installing the desktop runtime.")
	if err = m.installRuntime(ctx); err != nil {
		return err
	}
	// The port is Host transport state, excluded from frozen tool identities.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	p.config.Endpoint = "http://127.0.0.1:" + strconv.Itoa(port)
	p.client = &http.Client{Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 30 * time.Second}, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var saved struct {
		PasswordRef string `json:"password_ref"`
		HostKey     string `json:"host_key"`
	}
	credential := filepath.Join(m.root, "guest.json")
	if _, err = os.Lstat(credential); err == nil {
		if readJSON(credential, &saved) != nil {
			return computer.ErrIntegrity
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if saved.PasswordRef == "" {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		password := []byte(base64.RawURLEncoding.EncodeToString(key))
		clear(key)
		defer clear(password)
		saved.PasswordRef = "credential:desktop-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err = m.secrets.Put(ctx, saved.PasswordRef, password); err != nil {
			return err
		}
		if err = replaceManagedJSON(credential, saved); err != nil {
			return err
		}
	}
	password, err := m.secrets.Get(ctx, saved.PasswordRef)
	if err != nil || len(password) == 0 {
		return computer.ErrUnavailable
	}
	clear(password)
	p.config.Guest = SSHConfig{User: "lume", Port: 22, PasswordRef: saved.PasswordRef, HostKeySHA256: saved.HostKey}
	if _, err = os.Lstat(filepath.Join(p.config.Root, "steward-computer.json")); os.IsNotExist(err) {
		m.status("preparing", "Downloading and verifying the private macOS image. The download can take several minutes.")
		seed := filepath.Join(m.root, "seed", "vms", "default-base")
		verified := filepath.Join(m.root, "seed", "verified.json")
		var source struct {
			Digest string `json:"digest"`
		}
		if readJSON(verified, &source) != nil || source.Digest != defaultImageDigest {
			if err = m.writeSettings(filepath.Join(m.root, "seed", "vms")); err != nil {
				return err
			}
			if err = m.command(ctx, "pull", defaultImage, "default-base", "--storage", filepath.Join(m.root, "seed", "vms")).Run(); err != nil {
				return computer.ErrUnavailable
			}
			if err = verifyDefaultImage(ctx, seed); err != nil {
				return err
			}
			if err = replaceManagedJSON(verified, map[string]string{"digest": defaultImageDigest}); err != nil {
				return err
			}
		}
		if err = m.writeSettings(filepath.Join(m.root, "seed", "vms")); err != nil {
			return err
		}
		if err = m.startServer(ctx, p, filepath.Join(m.root, "seed", "vms")); err != nil {
			return err
		}
		m.status("preparing", "Starting macOS and preparing private Guest access.")
		status, _, e := p.state(ctx, "default-base")
		if e != nil {
			return e
		}
		if status == "stopped" {
			if _, code, e := p.api(ctx, "POST", "/vms/default-base/run", map[string]any{"noDisplay": true, "vnc": "disabled"}); e != nil || code != 200 && code != 202 {
				return computer.ErrUnavailable
			}
		}
		var ip string
		for {
			_, ip, e = p.state(ctx, "default-base")
			if e == nil && ip != "" {
				break
			}
			if e = pause(ctx); e != nil {
				return e
			}
		}
		if err = p.bootstrapGuest(ctx, ip, &saved, credential); err != nil {
			return err
		}
		g := SSHGuest{Config: p.config.Guest, Secrets: m.secrets}
		p.guest = g
		if err = g.Ready(ctx, ip, p.config.Compatibility); err != nil {
			return err
		}
		if err = g.Shutdown(ctx, ip); err != nil {
			return err
		}
		for {
			f, e := quiescent(seed, true)
			if e == nil {
				f.Close()
				break
			}
			if !errors.Is(e, computer.ErrBusy) {
				return e
			}
			if e = pause(ctx); e != nil {
				return e
			}
		}
		_, _, _ = p.api(ctx, "POST", "/vms/default-base/stop", map[string]bool{"force": false})
		if err = m.stopServer(); err != nil {
			return err
		}
		stage := filepath.Join(m.root, ".store-stage")
		if err = os.RemoveAll(stage); err != nil {
			return err
		}
		if err = os.Mkdir(stage, 0700); err != nil {
			return err
		}
		if err = ProvisionBase(ctx, stage, seed, p.config.Compatibility); err != nil {
			return err
		}
		if err = os.Rename(stage, p.config.Root); err != nil {
			return err
		}
		if err = syncDir(m.root); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if p.config.Guest.Validate() != nil {
		return computer.ErrIntegrity
	}
	p.guest = SSHGuest{Config: p.config.Guest, Secrets: m.secrets}
	if err = m.writeSettings(filepath.Join(p.config.Root, "vms")); err != nil {
		return err
	}
	if err = m.startServer(ctx, p, filepath.Join(p.config.Root, "vms")); err != nil {
		return err
	}
	if err = p.Probe(ctx); err != nil {
		return err
	}
	m.status("ready", "The private macOS desktop is ready.")
	return nil
}

func (m *managedRuntime) writeSettings(vms string) error {
	// All strings are generated absolute paths; reject line breaks for Lume's
	// deliberately small YAML parser rather than allowing a second setting.
	if strings.ContainsAny(m.root, "\n\r\"\\") {
		return computer.ErrInvalid
	}
	text := "defaultLocationName: \"steward\"\ncacheDirectory: \"" + filepath.Join(m.root, "cache") + "\"\ncachingEnabled: false\ntelemetryEnabled: false\nvmLocations:\n  - name: \"steward\"\n    path: \"" + vms + "\"\n"
	return os.WriteFile(filepath.Join(m.root, "config", "lume", "config.yaml"), []byte(text), 0600)
}

func (m *managedRuntime) startServer(ctx context.Context, p *Provider, vms string) error {
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(p.config.Endpoint, "http://"))
	m.server = m.command(m.lifetime, "serve", "--port", port)
	if err := m.server.Start(); err != nil {
		m.server = nil
		return computer.ErrUnavailable
	}
	m.serverDone = make(chan error, 1)
	cmd := m.server
	go func() { m.serverDone <- cmd.Wait() }()
	for {
		select {
		case <-m.serverDone:
			m.server = nil
			return computer.ErrUnavailable
		default:
		}
		if p.checkHostStore(ctx, vms) == nil {
			return nil
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
}

func (m *managedRuntime) stopServer() error {
	if m.server == nil {
		return nil
	}
	cmd := m.server
	m.server = nil
	_ = cmd.Process.Signal(os.Interrupt)
	var err error
	select {
	case err = <-m.serverDone:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		err = <-m.serverDone
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return nil
	}
	return err
}

func (p *Provider) bootstrapGuest(ctx context.Context, ip string, saved *struct {
	PasswordRef string `json:"password_ref"`
	HostKey     string `json:"host_key"`
}, path string) error {
	m := p.managed
	// The first key comes only from our checksum-verified clean seed. Pin it
	// before any private credentials are sent, and retain it across restart.
	if saved.HostKey == "" {
		for {
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, "22"), 5*time.Second)
			if err == nil {
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				cfg := &ssh.ClientConfig{User: "lume", HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
					saved.HostKey = ssh.FingerprintSHA256(key)
					return computer.ErrUnavailable
				}}
				_, _, _, _ = ssh.NewClientConn(conn, net.JoinHostPort(ip, "22"), cfg)
				conn.Close()
				if saved.HostKey != "" {
					break
				}
			}
			if err = pause(ctx); err != nil {
				return err
			}
		}
		if err := replaceManagedJSON(path, saved); err != nil {
			return err
		}
	}
	p.config.Guest.HostKeySHA256 = saved.HostKey
	privateGuest := SSHGuest{Config: p.config.Guest, Secrets: m.secrets}
	if _, err := privateGuest.command(ctx, ip, "/usr/bin/true", false); err == nil {
		return nil
	}
	seedGuest := privateGuest
	seedGuest.Secrets = seedCredentials{}
	key, err := m.secrets.Get(ctx, saved.PasswordRef)
	if err != nil {
		return err
	}
	defer clear(key)
	input, _ := json.Marshal(map[string]string{"password": string(key)})
	defer clear(input)
	_, err = seedGuest.commandInput(ctx, ip, "/usr/bin/python3 -I -c "+quote(changeSeedPassword), false, strings.NewReader(string(input)))
	return err
}

// Credential references and first-use trust pins must commit together before
// rotating the seed password. A crash leaves either complete old or new data.
func replaceManagedJSON(path string, value any) error {
	parent := filepath.Dir(path)
	if privateRoot(parent) != nil {
		return computer.ErrIntegrity
	}
	dir, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	id := make([]byte, 12)
	if _, err = rand.Read(id); err != nil {
		return err
	}
	name := ".metadata-" + base64.RawURLEncoding.EncodeToString(id)
	f, err := dir.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer dir.Remove(name)
	raw, err := json.Marshal(value)
	if err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = dir.Rename(name, filepath.Base(path)); err != nil {
		return err
	}
	d, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

type seedCredentials struct{}

func (seedCredentials) Get(context.Context, string) ([]byte, error) { return []byte("lume"), nil }
func (seedCredentials) Put(context.Context, string, []byte) error   { return llm.ErrSecretStore }
func (seedCredentials) Delete(context.Context, string) error        { return llm.ErrSecretStore }

const changeSeedPassword = `import json,os,pty,select,sys,time
password=json.load(sys.stdin)['password']
pid,fd=pty.fork()
if pid==0:
 os.execl('/usr/bin/passwd','passwd')
buffer=b''
deadline=time.monotonic()+20
while time.monotonic()<deadline:
 if select.select([fd],[],[],.1)[0]:
  try: buffer+=os.read(fd,4096)
  except OSError: break
  if b'Old Password:' in buffer:
   os.write(fd,b'lume\n');buffer=b''
  elif b'Retype New Password:' in buffer or b'Retype new password:' in buffer:
   os.write(fd,password.encode()+b'\n');buffer=b''
  elif b'New Password:' in buffer or b'New password:' in buffer:
   os.write(fd,password.encode()+b'\n');buffer=b''
 else:
  done,status=os.waitpid(pid,os.WNOHANG)
  if done: sys.exit(os.waitstatus_to_exitcode(status))
else:
 os.kill(pid,9)
sys.exit(os.waitstatus_to_exitcode(os.waitpid(pid,0)[1]))
`
