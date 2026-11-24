package macosvm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/llm"
	"golang.org/x/crypto/ssh"
)

type SSHConfig struct {
	User          string `yaml:"user" json:"user"`
	Port          int    `yaml:"port" json:"port"`
	PasswordRef   string `yaml:"password_ref" json:"password_ref"`
	HostKeySHA256 string `yaml:"host_key_sha256" json:"host_key_sha256"`
}

var sshUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,63}$`)

func (c SSHConfig) Validate() error {
	if !sshUser.MatchString(c.User) || c.Port < 1 || c.Port > 65535 || c.PasswordRef == "" || len(c.PasswordRef) > 512 || !strings.HasPrefix(c.HostKeySHA256, "SHA256:") || len(c.HostKeySHA256) != 50 {
		return computer.ErrInvalid
	}
	return nil
}

// SSHGuest resolves only the explicit credential reference, in memory. It never
// consults ~/.ssh, an ssh-agent, or a personal keychain item selected by discovery.
type SSHGuest struct {
	Config  SSHConfig
	Secrets llm.SecretStore
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

type boundedOutput struct {
	strings.Builder
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, computer.ErrUnavailable
	}
	return b.Builder.Write(p)
}

func (g SSHGuest) command(ctx context.Context, ip, command string, sudo bool) (string, error) {
	return g.commandInput(ctx, ip, command, sudo, nil)
}
func (g SSHGuest) commandInput(ctx context.Context, ip, command string, sudo bool, input io.Reader) (string, error) {
	return g.commandInputFor(ctx, ip, command, sudo, input, 30*time.Second)
}
func (g SSHGuest) commandInputFor(ctx context.Context, ip, command string, sudo bool, input io.Reader, limit time.Duration) (string, error) {
	address := net.ParseIP(ip)
	if g.Config.Validate() != nil || g.Secrets == nil || address == nil || !address.IsPrivate() && !address.IsLoopback() {
		return "", computer.ErrInvalid
	}
	password, err := g.Secrets.Get(ctx, g.Config.PasswordRef)
	defer clear(password)
	if err != nil || len(password) == 0 || strings.ContainsAny(string(password), "\x00\r\n") {
		return "", computer.ErrUnavailable
	}
	hostMismatch := false
	cfg := &ssh.ClientConfig{User: g.Config.User, Auth: []ssh.AuthMethod{ssh.Password(string(password))}, HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}, Timeout: 5 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != g.Config.HostKeySHA256 {
			hostMismatch = true
			return computer.ErrCompatibility
		}
		return nil
	}}
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(g.Config.Port)))
	if err != nil {
		return "", computer.ErrUnavailable
	}
	defer connection.Close()
	deadline := time.Now().Add(limit)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return "", computer.ErrUnavailable
	}
	joined := make(chan struct{})
	defer close(joined)
	go func() {
		select {
		case <-ctx.Done():
			connection.Close()
		case <-joined:
		}
	}()
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, net.JoinHostPort(ip, strconv.Itoa(g.Config.Port)), cfg)
	if err != nil {
		if hostMismatch {
			return "", computer.ErrCompatibility
		}
		return "", computer.ErrUnavailable
	}
	client := ssh.NewClient(clientConnection, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", computer.ErrUnavailable
	}
	defer session.Close()
	session.Stdin = input
	if sudo {
		session.Stdin = strings.NewReader(string(password) + "\n")
	}
	output := &boundedOutput{limit: 12 * 1024 * 1024}
	session.Stdout, session.Stderr = output, io.Discard
	if err := session.Run(command); err != nil {
		var exit *ssh.ExitError
		if errors.As(err, &exit) && exit.ExitStatus() == 42 {
			return "", computer.ErrCompatibility
		}
		// Never expose raw SSH errors, command text, IPs or credential bytes.
		return "", computer.ErrUnavailable
	}
	return output.String(), nil
}

func (g SSHGuest) Ready(ctx context.Context, ip string, compatibility computer.Compatibility) error {
	const driver = "/Applications/CuaDriver.app/Contents/MacOS/cua-driver"
	command := "set -eu\n" +
		"test \"$(/usr/bin/stat -f %Su /dev/console)\" = " + quote(g.Config.User) + " || exit 75\n" +
		"test \"$(/usr/bin/sw_vers -buildVersion)\" = " + quote(compatibility.GuestBuild) + " || exit 42\n" +
		"test \"$(/usr/bin/uname -m)\" = " + quote(compatibility.Architecture) + " || exit 42\n" +
		"/usr/bin/codesign --verify --strict -R " + quote(`=anchor apple generic and identifier "`+compatibility.DriverBundle+`" and certificate leaf[subject.OU] = "`+compatibility.DriverTeam+`"`) + " /Applications/CuaDriver.app || exit 42\n" +
		"test \"$(/usr/bin/shasum -a 256 " + driver + " | /usr/bin/awk '{print $1}')\" = " + quote(compatibility.DriverSHA256) + " || exit 42\n" +
		"test \"$(" + driver + " --version)\" = " + quote("cua-driver "+compatibility.DriverVersion) + " || exit 42\n" +
		driver + " permissions grant >/dev/null\n" +
		driver + " call get_desktop_state '{}'"
	for {
		output, err := g.command(ctx, ip, command, false)
		if err == computer.ErrCompatibility {
			return err
		}
		if err == nil {
			if captureReady(output) {
				return nil
			}
			return computer.ErrCompatibility
		}
		if err := pause(ctx); err != nil {
			return err
		}
	}
}

func captureReady(raw string) bool {
	var capture struct {
		ID  string `json:"capture_id"`
		PNG string `json:"screenshot_png_b64"`
	}
	return json.Unmarshal([]byte(raw), &capture) == nil && capture.ID != "" && capture.PNG != ""
}

func (g SSHGuest) Reopen(ctx context.Context, ip string, manifest computer.RestorationManifest) error {
	if manifest.Validate() != nil {
		return computer.ErrInvalid
	}
	for _, app := range manifest.Applications {
		command := "/usr/bin/open -b " + quote(app.BundleID)
		for _, document := range app.Documents {
			command += " " + quote(document)
		}
		if _, err := g.command(ctx, ip, command, false); err != nil {
			return err
		}
	}
	return nil
}

func (g SSHGuest) Shutdown(ctx context.Context, ip string) error {
	// The delayed Guest shutdown lets SSH acknowledge acceptance. Completion is
	// proved independently by the provider's native lock/session checks.
	_, err := g.command(ctx, ip, "/usr/bin/sudo -S -p '' /bin/sh -c 'nohup /bin/sh -c \"sleep 1; /sbin/shutdown -h now\" >/dev/null 2>&1 </dev/null &'", true)
	return err
}
