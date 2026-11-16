// Package fixture is the explicit offline Lume/Guest protocol Fake. It never
// executes received Guest commands on the Host and never starts a virtual machine.
package fixture

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/z-chenhao/assistant/internal/computer"
	computerfake "github.com/z-chenhao/assistant/internal/computer/fake"
	"github.com/z-chenhao/assistant/internal/computer/macosvm"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

type Server struct {
	previews                    int
	selectionCaret              int
	selectionAnchor             int
	selectionActive             bool
	use                         *computerfake.Driver
	Config                      macosvm.Config
	mu                          sync.Mutex
	locks                       map[string]*os.File
	running                     map[string]bool
	connections                 map[net.Conn]bool
	HTTP                        *httptest.Server
	listener                    net.Listener
	wait                        sync.WaitGroup
	closing                     bool
	browserEndpoint             string
	runs, shuts, stops, reopens int
}

func New(root string) (*Server, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, computer.ErrInvalid
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, computer.ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, computer.ErrInvalid
	}
	s := &Server{use: newUseFake(), locks: map[string]*os.File{}, running: map[string]bool{}, connections: map[net.Conn]bool{}}
	// Opt-in L1 peer: only the browser protocol crosses loopback. Received
	// Guest shell commands are still never executed on the Host.
	if data, e := os.ReadFile(filepath.Join(root, "browser-peer.json")); e == nil {
		var peer struct {
			Endpoint string `json:"endpoint"`
		}
		if json.Unmarshal(data, &peer) != nil || !browserPeerURL(peer.Endpoint) {
			return nil, computer.ErrInvalid
		}
		s.browserEndpoint = peer.Endpoint
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	s.HTTP = httptest.NewServer(http.HandlerFunc(s.serve))
	s.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.HTTP.Close()
		return nil, err
	}
	seed := sha256.Sum256([]byte("STEWARD explicit offline Guest host key fixture, never a real credential"))
	signer, err := ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(seed[:]))
	if err != nil {
		s.Close()
		return nil, err
	}
	port, _ := strconv.Atoi(strings.Split(s.listener.Addr().String(), ":")[1])
	s.Config = macosvm.Config{Root: root, Endpoint: s.HTTP.URL, Compatibility: computer.Compatibility{ProviderVersion: "0.6.1", GuestBuild: "25F84", DriverVersion: "0.36.0", DriverSHA256: strings.Repeat("a", 64), DriverTeam: "YCK386LBJ7", DriverBundle: "com.trycua.driver", Architecture: "arm64"}, Manifest: computer.RestorationManifest{Version: 1}, Guest: macosvm.SSHConfig{User: "lume", Port: port, PasswordRef: "env:STEWARD_COMPUTER_FIXTURE_PASSWORD", HostKeySHA256: ssh.FingerprintSHA256(signer.PublicKey())}}
	s.Config.Browser = &macosvm.BrowserConfig{Node: "/opt/steward/node", Module: "/opt/steward/playwright/index.mjs", Port: 9222}
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if meta.User() != "lume" || string(password) != "fixture-only" {
			return nil, errors.New("fixture authentication rejected")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	// A real macOS SSH server also offers ECDSA. Its higher default preference
	// must not override the frozen Ed25519 identity.
	d := new(big.Int).SetBytes(seed[:])
	curve := elliptic.P256()
	d.Mod(d, new(big.Int).Sub(curve.Params().N, big.NewInt(1)))
	d.Add(d, big.NewInt(1))
	key, err := ecdsa.ParseRawPrivateKey(curve, d.FillBytes(make([]byte, 32)))
	if err != nil {
		s.Close()
		return nil, err
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(key)
	if err != nil {
		s.Close()
		return nil, err
	}
	config.AddHostKey(ecdsaSigner)
	s.wait.Add(1)
	go func() {
		defer s.wait.Done()
		for {
			connection, err := s.listener.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			if s.closing {
				s.mu.Unlock()
				connection.Close()
				return
			}
			s.connections[connection] = true
			s.wait.Add(1)
			s.mu.Unlock()
			go s.ssh(connection, config)
		}
	}()
	return s, nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	s.closing = true
	for connection := range s.connections {
		connection.Close()
	}
	s.mu.Unlock()
	if s.listener != nil {
		s.listener.Close()
	}
	if s.HTTP != nil {
		s.HTTP.Close()
	}
	s.wait.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, lock := range s.locks {
		lock.Close()
		os.Remove(filepath.Join(s.Config.Root, "vms", name, "sessions.json"))
	}
	return nil
}

func (s *Server) ssh(connection net.Conn, config *ssh.ServerConfig) {
	defer s.wait.Done()
	defer connection.Close()
	defer func() { s.mu.Lock(); delete(s.connections, connection); s.mu.Unlock() }()
	server, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for incoming := range channels {
		if incoming.ChannelType() != "session" {
			incoming.Reject(ssh.UnknownChannelType, "fixture supports sessions only")
			continue
		}
		channel, requests, err := incoming.Accept()
		if err != nil {
			continue
		}
		for request := range requests {
			if request.Type != "exec" {
				request.Reply(false, nil)
				continue
			}
			var payload struct{ Command string }
			if ssh.Unmarshal(request.Payload, &payload) != nil {
				request.Reply(false, nil)
				continue
			}
			request.Reply(true, nil)
			exit := uint32(0)
			shutdown := strings.HasPrefix(payload.Command, "/usr/bin/sudo -S -p '' /bin/sh -c 'nohup ")
			if shutdown {
				// Real sudo consumes its password before the command can exit.
				// Closing this channel first races the SSH client's stdin copy,
				// which correctly makes an otherwise successful exit uncertain.
				password := make([]byte, len("fixture-only\n"))
				if _, err := io.ReadFull(channel, password); err != nil || string(password) != "fixture-only\n" {
					exit = 1
				}
			}
			var input []byte
			if strings.HasPrefix(payload.Command, "/usr/bin/python3 -c ") {
				input, _ = io.ReadAll(io.LimitReader(channel, 12<<20))
			}
			s.mu.Lock()
			switch {
			case exit != 0:
			case strings.HasPrefix(payload.Command, "set -eu\n") && strings.Contains(payload.Command, "codesign --verify --strict") && strings.Contains(payload.Command, "permissions grant"):
				if !strings.Contains(payload.Command, `-R '=anchor apple generic and identifier "com.trycua.driver" and certificate leaf[subject.OU] = "YCK386LBJ7"'`) || !strings.Contains(payload.Command, "'"+s.Config.Compatibility.DriverSHA256+"'") || !strings.Contains(payload.Command, "'"+s.Config.Compatibility.GuestBuild+"'") {
					exit = 42
				} else {
					io.WriteString(channel, `{"capture_id":"fixture-current-capture","screenshot_png_b64":"RklYVFVSRQ=="}`)
				}
			case strings.HasPrefix(payload.Command, "/usr/bin/open -b "):
				s.reopens++
			case shutdown:
				s.shuts++
				for name, lock := range s.locks {
					lock.Close()
					delete(s.locks, name)
					os.Remove(filepath.Join(s.Config.Root, "vms", name, "sessions.json"))
				}
			default:
				if value, ok := s.useCommand(payload.Command, string(input)); ok {
					json.NewEncoder(channel).Encode(value)
				} else {
					exit = 1
				}
			}
			s.mu.Unlock()
			channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{exit}))
			channel.Close()
			break
		}
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	root := s.Config.Root
	if r.URL.Path == "/lume/vms/clone" && r.Method == "POST" {
		var input struct {
			Name    string `json:"name"`
			NewName string `json:"newName"`
			Source  string `json:"sourceLocation"`
			Dest    string `json:"destLocation"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Name != "base" || !strings.HasPrefix(input.NewName, "steward-") || strings.ContainsAny(input.NewName, "/\\.") || input.Source != root || input.Dest != filepath.Join(root, "vms") {
			http.Error(w, "unsafe clone", 400)
			return
		}
		dir := filepath.Join(input.Dest, input.NewName)
		if os.Mkdir(dir, 0700) != nil {
			http.Error(w, "clone exists", 400)
			return
		}
		for _, file := range []string{"disk.img", "nvram.bin", "config.json"} {
			raw, err := os.ReadFile(filepath.Join(root, "base", file))
			if err != nil || os.WriteFile(filepath.Join(dir, file), raw, 0600) != nil {
				http.Error(w, "clone copy", 500)
				return
			}
		}
		raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
		var config map[string]any
		if err != nil || json.Unmarshal(raw, &config) != nil {
			http.Error(w, "config", 500)
			return
		}
		seed := sha256.Sum256([]byte(input.NewName))
		config["macAddress"] = net.HardwareAddr(append([]byte{2}, seed[:5]...)).String()
		config["machineIdentifier"] = "fixture-" + input.NewName
		raw, err = json.Marshal(config)
		if err != nil || os.WriteFile(filepath.Join(dir, "config.json"), raw, 0600) != nil {
			http.Error(w, "config", 500)
			return
		}
		w.Write([]byte(`{}`))
		return
	}
	if r.URL.Path == "/fixture/status" {
		json.NewEncoder(w).Encode(map[string]int{"running": len(s.locks), "runs": s.runs, "shutdowns": s.shuts, "stops": s.stops, "reopens": s.reopens, "previews": s.previews})
		return
	}
	if r.URL.Path == "/fixture/write" && r.Method == "POST" {
		if len(s.locks) != 1 {
			http.Error(w, "one running fixture required", http.StatusConflict)
			return
		}
		var input struct {
			Disk   string
			NVRAM  string
			Config string
		}
		d := json.NewDecoder(io.LimitReader(r.Body, 65536))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil {
			http.Error(w, "invalid fixture", 400)
			return
		}
		for name := range s.locks {
			for file, data := range map[string]string{"disk.img": input.Disk, "nvram.bin": input.NVRAM, "config.json": input.Config} {
				if os.WriteFile(filepath.Join(root, "vms", name, file), []byte(data), 0600) != nil {
					http.Error(w, "fixture write failed", 500)
					return
				}
			}
		}
		w.Write([]byte(`{}`))
		return
	}
	if r.URL.Path == "/lume/config" {
		json.NewEncoder(w).Encode(map[string]any{"telemetryEnabled": false, "cachingEnabled": false, "vmLocations": []any{map[string]string{"name": "owned", "path": filepath.Join(root, "vms")}}})
		return
	}
	if r.URL.Path == "/lume/host/status" {
		w.Write([]byte(`{"version":"0.6.1"}`))
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/lume/vms/"), "/")
	if len(parts) == 0 || strings.ContainsAny(parts[0], ".\\") || !strings.HasPrefix(parts[0], "steward-") {
		http.Error(w, "invalid fixture VM", 400)
		return
	}
	name := parts[0]
	dir := filepath.Join(root, "vms", name)
	if r.Method == "GET" && len(parts) == 1 {
		if _, err := os.Stat(dir); err != nil {
			http.Error(w, "missing fixture", 400)
			return
		}
		status := "stopped"
		if s.running[name] {
			status = "running"
		}
		json.NewEncoder(w).Encode(map[string]string{"status": status, "ipAddress": "127.0.0.1"})
		return
	}
	if r.Method == "DELETE" && len(parts) == 1 {
		if s.locks[name] != nil {
			http.Error(w, "fixture running", http.StatusConflict)
			return
		}
		if os.RemoveAll(dir) != nil {
			http.Error(w, "fixture delete", 500)
			return
		}
		delete(s.running, name)
		w.Write([]byte(`{}`))
		return
	}
	if len(parts) != 2 {
		http.Error(w, "invalid fixture path", 400)
		return
	}
	switch parts[1] {
	case "run":
		var input map[string]any
		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input) != 2 || input["noDisplay"] != true || input["vnc"] != "disabled" {
			http.Error(w, "unsafe fixture run", 400)
			return
		}
		lock, err := os.Open(filepath.Join(dir, "config.json"))
		if err != nil {
			http.Error(w, "fixture config", 500)
			return
		}
		if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
			lock.Close()
			http.Error(w, "fixture locked", http.StatusConflict)
			return
		}
		s.runs++
		s.locks[name] = lock
		s.running[name] = true
		os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`{"secret":"excluded-fixture-session"}`), 0600)
		w.WriteHeader(202)
		w.Write([]byte(`{"status":"pending"}`))
	case "stop":
		if s.locks[name] != nil {
			http.Error(w, "unsafe poweroff", http.StatusConflict)
			return
		}
		if _, err := os.Stat(filepath.Join(dir, "sessions.json")); !os.IsNotExist(err) {
			http.Error(w, "fixture marker retained", http.StatusConflict)
			return
		}
		s.stops++
		s.running[name] = false
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"message": "Virtual machine not running: " + name})
	default:
		http.Error(w, "invalid fixture operation", 400)
	}
}

func Serve(ctx context.Context, root string, out io.Writer) error {
	s, err := New(root)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := json.NewEncoder(out).Encode(s.Config); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
