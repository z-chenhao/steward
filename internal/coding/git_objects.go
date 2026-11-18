package coding

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// GitContent is an explicit owner-only local object observation. Its head must
// also be fenced against the trusted remote observation by the caller. Reading
// this object alone says nothing about remote ownership or human acceptance.
type GitContent struct {
	Head             string `json:"head"`
	TreeSHA256       string `json:"tree_sha256"`
	Driver           string `json:"driver"`
	GitVersion       string `json:"git_version"`
	GitCommandSHA256 string `json:"git_command_sha256"`
}

//go:embed git_objects.go
var gitObjectDriver string

func gitReadEnvironment() []string {
	// No inherited credentials, Git config, trace destinations, replace refs or
	// helpers. The empty transport allowlist is authoritative even on Git versions
	// predating GIT_NO_LAZY_FETCH; missing objects cannot open any remote transport.
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=/nonexistent", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL="}
}

// ReadGitContent reads raw objects only: no checkout, hooks, filters, textconv,
// branch names, network or source imports. Hashes of every returned object are
// independently checked, then canonicalized exactly as CaptureCandidate.
func ReadGitContent(ctx context.Context, source, head string) (GitContent, error) {
	var out GitContent
	if !filepath.IsAbs(source) || filepath.Clean(source) != source || (len(head) != 40 && len(head) != 64) {
		return out, ErrInvalid
	}
	digest, err := hex.DecodeString(head)
	if err != nil {
		return out, ErrInvalid
	}
	head = strings.ToLower(head)
	path, err := exec.LookPath("git")
	if err != nil {
		return out, ErrInvalid
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return out, ErrInvalid
	}
	binary, err := os.Open(path)
	if err != nil {
		return out, ErrInvalid
	}
	sum := sha256.New()
	_, err = io.Copy(sum, binary)
	binary.Close()
	if err != nil {
		return out, ErrInvalid
	}
	out.GitCommandSHA256 = hex.EncodeToString(sum.Sum(nil))
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	version := exec.CommandContext(ctx, path, "--version")
	version.Env = gitReadEnvironment()
	raw, err := version.Output()
	if err != nil || len(raw) > 256 {
		return GitContent{}, ErrInvalid
	}
	out.GitVersion = strings.TrimSpace(string(raw))
	command := exec.CommandContext(ctx, path, "--no-replace-objects", "-c", "core.hooksPath=/dev/null", "-c", "maintenance.auto=false", "-c", "gc.auto=0", "-C", source, "cat-file", "--batch")
	command.Env = gitReadEnvironment()
	stdin, err := command.StdinPipe()
	if err != nil {
		return GitContent{}, ErrInvalid
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return GitContent{}, ErrInvalid
	}
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		return GitContent{}, ErrInvalid
	}
	done := false
	defer func() {
		if !done {
			cancel()
			_ = command.Wait()
		}
	}()
	reader := bufio.NewReaderSize(stdout, 4096)
	read := func(id, kind string) ([]byte, error) { return readGitObject(reader, stdin, id, kind, len(digest)) }

	commit, err := read(head, "commit")
	if err != nil {
		return GitContent{}, err
	}
	line, _, _ := bytes.Cut(commit, []byte("\n"))
	tree, ok := strings.CutPrefix(string(line), "tree ")
	if !ok || len(tree) != len(head) {
		return GitContent{}, ErrInvalid
	}
	if _, err = hex.DecodeString(tree); err != nil {
		return GitContent{}, ErrInvalid
	}
	type entry struct{ name, mode, id string }
	var files []manifestFile
	visited, total := 0, 0
	var walk func(string, string, int) error
	walk = func(id, dir string, depth int) error {
		if depth > 128 {
			return ErrInvalid
		}
		data, err := read(id, "tree")
		if err != nil {
			return err
		}
		var entries []entry
		seen := map[string]bool{}
		for len(data) > 0 {
			part, rest, ok := bytes.Cut(data, []byte{0})
			if !ok || len(rest) < len(digest) {
				return ErrInvalid
			}
			mode, name, ok := strings.Cut(string(part), " ")
			if !ok || name == "" || name == "." || name == ".." || strings.EqualFold(name, ".git") || strings.ContainsAny(name, "/\r\n\x00") || !utf8.ValidString(name) || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
			visited++
			if visited > maxFiles {
				return ErrInvalid
			}
			entries = append(entries, entry{name, mode, hex.EncodeToString(rest[:len(digest)])})
			data = rest[len(digest):]
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		for _, e := range entries {
			name := e.name
			if dir != "" {
				name = dir + "/" + name
			}
			if len(name) > 1024 {
				return ErrInvalid
			}
			if e.mode == "40000" {
				if err := walk(e.id, name, depth+1); err != nil {
					return err
				}
				continue
			}
			if e.mode != "100644" && e.mode != "100755" {
				return ErrInvalid
			}
			data, err := read(e.id, "blob")
			if err != nil {
				return err
			}
			total += len(data)
			if total > maxBytes {
				return ErrInvalid
			}
			sum := sha256.Sum256(data)
			mode := uint32(0600)
			if e.mode == "100755" {
				mode = 0700
			}
			files = append(files, manifestFile{Name: name, Hash: hex.EncodeToString(sum[:]), Mode: mode})
		}
		return nil
	}
	if err = walk(tree, "", 0); err != nil {
		return GitContent{}, err
	}
	if err = stdin.Close(); err != nil {
		return GitContent{}, ErrInvalid
	}
	// EOF plus the process exit are required; a valid prefix is insufficient.
	if _, err = reader.ReadByte(); err != io.EOF {
		return GitContent{}, ErrInvalid
	}
	err = command.Wait()
	done = true
	if err != nil {
		return GitContent{}, ErrInvalid
	}
	encoded, _ := json.Marshal(files)
	canonical := sha256.Sum256(encoded)
	out.Head = head
	out.TreeSHA256 = hex.EncodeToString(canonical[:])
	out.Driver = fmt.Sprintf("steward-git-content/1:%x", sha256.Sum256([]byte(gitObjectDriver)))
	return out, nil
}

func readGitObject(reader *bufio.Reader, stdin io.Writer, id, kind string, hashBytes int) ([]byte, error) {
	if _, err := io.WriteString(stdin, id+"\n"); err != nil {
		return nil, ErrInvalid
	}
	header, err := reader.ReadSlice('\n')
	if err != nil || len(header) > 256 {
		return nil, ErrInvalid
	}
	fields := strings.Fields(string(header))
	if len(fields) != 3 || fields[0] != id || fields[1] != kind {
		return nil, ErrInvalid
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 || size > 8<<20 {
		return nil, ErrInvalid
	}
	data := make([]byte, int(size))
	if _, err = io.ReadFull(reader, data); err != nil {
		return nil, ErrInvalid
	}
	sep, err := reader.ReadByte()
	if err != nil || sep != '\n' {
		return nil, ErrInvalid
	}
	var h hash.Hash = sha1.New()
	if hashBytes == 32 {
		h = sha256.New()
	}
	fmt.Fprintf(h, "%s %d\x00", kind, len(data))
	h.Write(data)
	if hex.EncodeToString(h.Sum(nil)) != id {
		return nil, ErrInvalid
	}
	return data, nil

}
