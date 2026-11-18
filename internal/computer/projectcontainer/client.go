// Package projectcontainer owns the narrow local Docker resource boundary for
// approved project verification. It contains no project selection policy.
package projectcontainer

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var ErrUnavailable = errors.New("project container unavailable")
var ErrInvalid = errors.New("incompatible project container")
var ErrUnknown = errors.New("project container effect unknown")
var ErrNotFound = errors.New("project container not found")
var ErrOwnership = errors.New("project container ownership mismatch")

const protocol = "/v1.44"
const ownerLabel = "ai.steward.project.invocation"
const isolationProfile = "steward-project-container/1"

type Config struct {
	Socket string `yaml:"socket" json:"socket"`
	Image  string `yaml:"image" json:"image"`
}

func hexID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func (c Config) Validate() error {
	if !filepath.IsAbs(c.Socket) || filepath.Clean(c.Socket) != c.Socket || strings.ContainsAny(c.Socket, "\x00\r\n") || !strings.HasPrefix(c.Image, "sha256:") || !hexID(strings.TrimPrefix(c.Image, "sha256:")) {
		return ErrInvalid
	}
	return nil
}
func (c Config) ResourceHash(scope string) string {
	raw, _ := json.Marshal(c)
	return fmt.Sprintf("steward-project-container/1:%x", sha256.Sum256(append([]byte(scope+"\x00"), raw...)))
}

type Client struct {
	config Config
	http   *http.Client
}

func New(c Config) (*Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	}, DisableCompression: true}
	return &Client{config: c, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrInvalid }}}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) request(ctx context.Context, method, path string, input any) (*http.Response, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, ErrInvalid
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://steward.local"+path, body)
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	return res, nil
}
func (c *Client) json(ctx context.Context, method, path string, input, output any, want int) error {
	res, err := c.request(ctx, method, path, input)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return ErrNotFound
	}
	if res.StatusCode != want {
		return ErrUnavailable
	}
	if output == nil {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return ErrInvalid
	}
	if json.Unmarshal(raw, output) != nil {
		return ErrInvalid
	}
	return nil
}

type Basis struct {
	Profile      string `json:"profile"`
	User         string `json:"user"`
	Image        string `json:"image"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Engine       string `json:"engine"`
	EngineCommit string `json:"engine_commit"`
	Kernel       string `json:"kernel"`
}

func (b Basis) Digest() string {
	raw, _ := json.Marshal(b)
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte("steward-project-environment/1\x00"), raw...)))
}
func apiNumber(s string) (int, error) {
	a, b, ok := strings.Cut(s, ".")
	if !ok {
		return 0, ErrInvalid
	}
	major, e := strconv.Atoi(a)
	minor, f := strconv.Atoi(b)
	if e != nil || f != nil {
		return 0, ErrInvalid
	}
	return major*1000 + minor, nil
}
func (c *Client) Basis(ctx context.Context) (Basis, error) {
	var version struct{ Version, ApiVersion, MinAPIVersion, GitCommit, KernelVersion string }
	if err := c.json(ctx, "GET", "/version", nil, &version, 200); err != nil {
		return Basis{}, err
	}
	current, e := apiNumber(version.ApiVersion)
	minimum, f := apiNumber(version.MinAPIVersion)
	if e != nil || f != nil || current < 1044 || minimum > 1044 || version.Version == "" || version.KernelVersion == "" {
		return Basis{}, ErrInvalid
	}
	image, err := c.image(ctx)
	if err != nil {
		return Basis{}, err
	}
	return Basis{Profile: isolationProfile, User: containerUser(), Image: image.ID, OS: image.OS, Arch: image.Arch, Engine: version.Version, EngineCommit: version.GitCommit, Kernel: version.KernelVersion}, nil
}

type imageIdentity struct {
	ID     string `json:"Id"`
	OS     string `json:"Os"`
	Arch   string `json:"Architecture"`
	Config struct{ Volumes map[string]json.RawMessage }
}

func (c *Client) image(ctx context.Context) (imageIdentity, error) {
	var image imageIdentity
	if err := c.json(ctx, "GET", protocol+"/images/"+url.PathEscape(c.config.Image)+"/json", nil, &image, 200); err != nil {
		return image, err
	}
	// Image-declared volumes would create extra writable stores outside the
	// candidate. They cannot be removed by a container Config override.
	if image.ID != c.config.Image || image.OS != "linux" || (image.Arch != "arm64" && image.Arch != "amd64") || len(image.Config.Volumes) != 0 {
		return image, ErrInvalid
	}
	return image, nil
}
func containerUser() string { return fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()) }

func Name(token string) string {
	return "steward-project-" + fmt.Sprintf("%x", sha256.Sum256([]byte(token)))[:32]
}
func (c *Client) Create(ctx context.Context, token, directory string, env []string) error {
	if token == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return ErrInvalid
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrInvalid
	}
	if _, err := c.image(ctx); err != nil {
		return err
	}
	request := struct {
		Image, User, WorkingDir string
		Entrypoint, Cmd, Env    []string
		Healthcheck             struct{ Test []string }
		Labels                  map[string]string
		HostConfig              struct {
			NetworkMode                          string
			ReadonlyRootfs                       bool
			CapDrop, SecurityOpt                 []string
			PidsLimit, Memory, NanoCpus, ShmSize int64
			Mounts                               []struct {
				Type, Source, Target string
				ReadOnly             bool
			}
			Tmpfs map[string]string
		}
	}{Image: c.config.Image, User: containerUser(), WorkingDir: "/workspace", Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{"exec sleep 2147483647"}, Env: env, Labels: map[string]string{ownerLabel: token}}
	// Image health checks are not part of the approved lifecycle.
	request.Healthcheck.Test = []string{"NONE"}
	request.HostConfig.NetworkMode = "none"
	request.HostConfig.ReadonlyRootfs = true
	request.HostConfig.CapDrop = []string{"ALL"}
	request.HostConfig.SecurityOpt = []string{"no-new-privileges:true"}
	request.HostConfig.PidsLimit = 512
	request.HostConfig.Memory = 4 << 30
	request.HostConfig.NanoCpus = 2e9
	request.HostConfig.ShmSize = 256 << 20
	request.HostConfig.Mounts = append(request.HostConfig.Mounts, struct {
		Type, Source, Target string
		ReadOnly             bool
	}{"bind", directory, "/workspace", false})
	request.HostConfig.Tmpfs = map[string]string{"/tmp": "rw,nosuid,nodev,size=268435456,mode=1777"}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.json(ctx, "POST", protocol+"/containers/create?name="+Name(token), request, &created, 201); err != nil {
		return ErrUnknown
	}
	if !hexID(created.ID) {
		return ErrUnknown
	}
	return nil
}
func (c *Client) owned(ctx context.Context, token string) error {
	var current struct {
		Image, Name string
		Config      struct{ Labels map[string]string }
	}
	if err := c.json(ctx, "GET", protocol+"/containers/"+Name(token)+"/json", nil, &current, 200); err != nil {
		return err
	}
	if current.Image != c.config.Image || current.Name != "/"+Name(token) || current.Config.Labels[ownerLabel] != token {
		return ErrOwnership
	}
	return nil
}
func (c *Client) Start(ctx context.Context, token string) error {
	if err := c.owned(ctx, token); err != nil {
		return err
	}
	if err := c.json(ctx, "POST", protocol+"/containers/"+Name(token)+"/start", nil, nil, 204); err != nil {
		return ErrUnknown
	}
	return nil
}

// Removal is cleanup authority over this exact original resource, never a
// replay of the approved command. The bool means creation was actually observed;
// absence alone cannot settle an earlier uncertain create request.
func (c *Client) Remove(ctx context.Context, token string) (bool, error) {
	err := c.owned(ctx, token)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	err = c.json(ctx, "DELETE", protocol+"/containers/"+Name(token)+"?force=true&v=true", nil, nil, 204)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	return true, err
}
func (c *Client) Exec(ctx context.Context, token, command string, output io.Writer) (int, error) {
	if err := c.owned(ctx, token); err != nil {
		return 0, err
	}
	var created struct {
		ID string `json:"Id"`
	}
	input := struct {
		AttachStdout, AttachStderr bool
		Cmd                        []string
	}{true, true, []string{"/bin/sh", "-c", command}}
	if err := c.json(ctx, "POST", protocol+"/containers/"+Name(token)+"/exec", input, &created, 201); err != nil || !hexID(created.ID) {
		return 0, ErrUnknown
	}
	res, err := c.request(ctx, "POST", protocol+"/exec/"+created.ID+"/start", struct{ Detach, Tty bool }{false, false})
	if err != nil {
		return 0, ErrUnknown
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return 0, ErrUnknown
	}
	for {
		var header [8]byte
		_, err := io.ReadFull(res.Body, header[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, ErrUnknown
		}
		if (header[0] != 1 && header[0] != 2) || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return 0, ErrUnknown
		}
		size := binary.BigEndian.Uint32(header[4:])
		if size > 32<<20 {
			return 0, ErrUnknown
		}
		if _, err := io.CopyN(output, res.Body, int64(size)); err != nil {
			return 0, ErrUnknown
		}
	}
	var exited struct {
		Running  *bool
		ExitCode *int
	}
	if err := c.json(ctx, "GET", protocol+"/exec/"+created.ID+"/json", nil, &exited, 200); err != nil || exited.Running == nil || *exited.Running || exited.ExitCode == nil {
		return 0, ErrUnknown
	}
	return *exited.ExitCode, nil
}

//go:embed client.go
var clientSource string

func DriverFingerprint() string {
	return fmt.Sprintf("steward-project-container-driver/1:%x", sha256.Sum256([]byte(clientSource+"\x00"+runnerSource)))
}
