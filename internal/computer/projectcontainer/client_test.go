package projectcontainer

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func protocolClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "stwpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socket := filepath.Join(root, "daemon.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	client, err := New(Config{Socket: socket, Image: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}
func ownedJSON(token string) string {
	return `{"Image":"sha256:` + strings.Repeat("a", 64) + `","Name":"/` + Name(token) + `","Config":{"Labels":{"` + ownerLabel + `":"` + token + `"}}}`
}
func TestCodingContainerCreateUsesExactPrivateResourceAndConstrainedProfile(t *testing.T) {
	var mu sync.Mutex
	var request map[string]any
	c := protocolClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, protocol+"/images/") {
			w.Write([]byte(`{"Id":"sha256:` + strings.Repeat("a", 64) + `","Os":"linux","Architecture":"arm64","Config":{}}`))
			return
		}
		if r.URL.Path != protocol+"/containers/create" || r.Method != "POST" || r.URL.Query().Get("name") != Name("original") {
			t.Error("unexpected operation", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid create")
		}
		w.WriteHeader(201)
		w.Write([]byte(`{"Id":"` + strings.Repeat("b", 64) + `"}`))
	})
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0700)
	if err := c.Create(context.Background(), "original", dir, []string{"HOME=/workspace"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	health := request["Healthcheck"].(map[string]any)["Test"].([]any)
	if len(health) != 1 || health[0] != "NONE" || request["User"] != containerUser() {
		t.Fatal(request)
	}
	host := request["HostConfig"].(map[string]any)
	if host["NetworkMode"] != "none" || host["ReadonlyRootfs"] != true || host["Memory"] != float64(4<<30) || host["PidsLimit"] != float64(512) {
		t.Fatal(host)
	}
	mounts := host["Mounts"].([]any)
	if len(mounts) != 1 || mounts[0].(map[string]any)["Source"] != dir || mounts[0].(map[string]any)["Target"] != "/workspace" || request["Image"] != c.config.Image {
		t.Fatal(request)
	}
}
func TestCodingContainerExecRequiresCompleteOriginalFramesAndTerminalFacts(t *testing.T) {
	for _, variant := range []string{"success", "bad-frame", "missing-exit", "still-running", "command-failure"} {
		t.Run(variant, func(t *testing.T) {
			c := protocolClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case protocol + "/containers/" + Name("original") + "/json":
					w.Write([]byte(ownedJSON("original")))
				case protocol + "/containers/" + Name("original") + "/exec":
					var payload struct{ Cmd []string }
					json.NewDecoder(r.Body).Decode(&payload)
					if len(payload.Cmd) != 3 || payload.Cmd[2] != "printf original" {
						t.Error("command changed", payload)
					}
					w.WriteHeader(201)
					w.Write([]byte(`{"Id":"` + strings.Repeat("c", 64) + `"}`))
				case protocol + "/exec/" + strings.Repeat("c", 64) + "/start":
					header := []byte{1, 0, 0, 0, 0, 0, 0, 0}
					binary.BigEndian.PutUint32(header[4:], 8)
					if variant == "bad-frame" {
						header[1] = 1
					}
					w.Write(header)
					w.Write([]byte("original"))
				case protocol + "/exec/" + strings.Repeat("c", 64) + "/json":
					result := `{"Running":false,"ExitCode":0}`
					if variant == "missing-exit" {
						result = `{"Running":false}`
					}
					if variant == "still-running" {
						result = `{"Running":true,"ExitCode":0}`
					}
					if variant == "command-failure" {
						result = `{"Running":false,"ExitCode":7}`
					}
					w.Write([]byte(result))
				default:
					w.WriteHeader(404)
				}
			})
			var output bytes.Buffer
			exit, err := c.Exec(context.Background(), "original", "printf original", &output)
			if variant == "success" || variant == "command-failure" {
				want := 0
				if variant == "command-failure" {
					want = 7
				}
				if err != nil || exit != want || output.String() != "original" {
					t.Fatal(exit, err, output.String())
				}
			} else if !errors.Is(err, ErrUnknown) {
				t.Fatal("incomplete facts became terminal", exit, err)
			}
		})
	}
}
func TestCodingContainerCleanupRejectsUnrelatedOwnershipAndAcceptsAbsence(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "wrong-owner", true: "absent"}[absent], func(t *testing.T) {
			c := protocolClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					t.Error("removed unowned resource")
				}
				if absent {
					w.WriteHeader(404)
				} else {
					w.Write([]byte(ownedJSON("other")))
				}
			})
			observed, err := c.Remove(context.Background(), "original")
			if observed {
				t.Fatal("absence or unrelated resource invented a creation observation")
			}
			if absent && err != nil || !absent && !errors.Is(err, ErrOwnership) {
				t.Fatal(err)
			}
		})
	}
}

func TestCodingContainerBasisBindsProfileAndRejectsImplicitImageVolumes(t *testing.T) {
	for _, variant := range []string{"success", "volumes", "wrong-image", "wrong-os", "wrong-arch", "incompatible-api", "incompatible-minimum"} {
		t.Run(variant, func(t *testing.T) {
			created := false
			c := protocolClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/version" {
					api, minimum := "1.54", "1.40"
					if variant == "incompatible-api" {
						api = "1.43"
					}
					if variant == "incompatible-minimum" {
						minimum = "1.45"
					}
					json.NewEncoder(w).Encode(map[string]string{"Version": "29.4.0", "ApiVersion": api, "MinAPIVersion": minimum, "GitCommit": "daa0cb7", "KernelVersion": "6.12.76-linuxkit"})
					return
				}
				if strings.HasPrefix(r.URL.Path, protocol+"/images/") {
					id, osName, arch := "sha256:"+strings.Repeat("a", 64), "linux", "arm64"
					config := map[string]any{}
					switch variant {
					case "volumes":
						config["Volumes"] = map[string]any{"/untracked-state": map[string]any{}}
					case "wrong-image":
						id = "sha256:" + strings.Repeat("b", 64)
					case "wrong-os":
						osName = "windows"
					case "wrong-arch":
						arch = "unknown"
					}
					json.NewEncoder(w).Encode(map[string]any{"Id": id, "Os": osName, "Architecture": arch, "Config": config})
					return
				}
				created = true
				w.WriteHeader(500)
			})
			basis, err := c.Basis(context.Background())
			if variant == "success" {
				if err != nil || basis.Profile != isolationProfile || basis.User != containerUser() || basis.Image != c.config.Image {
					t.Fatal(basis, err)
				}
				changed := basis
				changed.Profile += "-changed"
				if basis.Digest() == changed.Digest() {
					t.Fatal("profile omitted from identity")
				}
				return
			}
			if err == nil || created {
				t.Fatal("incompatible image/runtime accepted", basis, err)
			}
			if variant == "volumes" {
				dir := liveDirectory(t)
				if err := c.Create(context.Background(), "original", dir, nil); err == nil || created {
					t.Fatal("implicit writable volume was created", err)
				}
			}
		})
	}
}
