package macosvm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/computer"
)

const lumeArchiveURL = "https://github.com/trycua/cua/releases/download/lume-v0.6.1/lume-0.6.1-darwin-arm64.tar.gz"
const lumeArchiveSHA = "2675b7981b7d3eb525cd27f40a385c986052eb71b3360c0b2579644725f1bad8"

func download(ctx context.Context, url string, headers map[string]string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, computer.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, computer.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes {
		return nil, computer.ErrUnavailable
	}
	return raw, nil
}

func (m *managedRuntime) installRuntime(ctx context.Context) error {
	if info, err := os.Lstat(filepath.Join(m.root, "runtime", "lume.app", "Contents", "MacOS", "lume")); err == nil && info.Mode().IsRegular() {
		cmd := m.command(ctx, "--version")
		cmd.Stdout = nil
		raw, err := cmd.Output()
		if err == nil && strings.TrimSpace(string(raw)) == "0.6.1" {
			return nil
		}
		return computer.ErrCompatibility
	}
	raw, err := download(ctx, lumeArchiveURL, nil, 16<<20)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != lumeArchiveSHA {
		return computer.ErrIntegrity
	}
	stage, err := os.MkdirTemp(m.root, ".runtime-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = extractRuntime(raw, stage); err != nil {
		return err
	}
	return os.Rename(stage, filepath.Join(m.root, "runtime"))
}

func extractRuntime(raw []byte, root string) error {
	z, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return computer.ErrIntegrity
	}
	defer z.Close()
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	t := tar.NewReader(z)
	var total int64
	for {
		h, err := t.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return computer.ErrIntegrity
		}
		name := filepath.Clean(h.Name)
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return computer.ErrIntegrity
		}
		if h.Typeflag == tar.TypeDir {
			if err = dir.MkdirAll(name, 0700); err != nil {
				return err
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > 32<<20 {
			return computer.ErrIntegrity
		}
		total += h.Size
		if total > 64<<20 {
			return computer.ErrIntegrity
		}
		if err = dir.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if h.FileInfo().Mode()&0111 != 0 {
			mode = 0700
		}
		f, err := dir.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, t)
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
	}
}

type imageManifest struct {
	Annotations map[string]string `json:"annotations"`
	ConfigData  []byte            `json:"-"`
	Config      imageLayer        `json:"config"`
	Layers      []imageLayer      `json:"layers"`
}
type imageLayer struct {
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
}

func verifyDefaultImage(ctx context.Context, root string) error {
	const repository = "trycua/macos-tahoe-cua"
	raw, err := download(ctx, "https://ghcr.io/token?service=ghcr.io&scope=repository:"+repository+":pull", nil, 16<<10)
	if err != nil {
		return err
	}
	var token struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &token) != nil || token.Token == "" {
		return computer.ErrUnavailable
	}
	raw, err = download(ctx, "https://ghcr.io/v2/"+repository+"/manifests/sha256:"+defaultImageDigest, map[string]string{"Authorization": "Bearer " + token.Token, "Accept": "application/vnd.oci.image.manifest.v1+json"}, 1<<20)
	if err != nil {
		return err
	}
	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != defaultImageDigest {
		return computer.ErrIntegrity
	}
	var manifest imageManifest
	if json.Unmarshal(raw, &manifest) != nil {
		return computer.ErrIntegrity
	}
	manifest.ConfigData, err = download(ctx, "https://ghcr.io/v2/"+repository+"/blobs/"+manifest.Config.Digest, map[string]string{"Authorization": "Bearer " + token.Token}, 16<<10)
	if err != nil {
		return err
	}
	configHash := sha256.Sum256(manifest.ConfigData)
	if "sha256:"+hex.EncodeToString(configHash[:]) != manifest.Config.Digest {
		return computer.ErrIntegrity
	}
	return verifyImageFiles(ctx, root, manifest)
}

// Lume converts the OCI config into its native VMConfig JSON. Compare the
// conversion's fields rather than expecting the two serializations to hash alike.
func verifyImageConfig(root string, m imageManifest) error {
	var source map[string]any
	if json.Unmarshal(m.ConfigData, &source) != nil {
		return computer.ErrIntegrity
	}
	actual, err := nativeConfig(root)
	if err != nil {
		return err
	}
	expected := map[string]any{}
	for _, key := range []string{"os", "hardwareModel", "machineIdentifier"} {
		if source[key] == nil {
			return computer.ErrIntegrity
		}
		expected[key] = source[key]
	}
	for _, key := range []string{"cpuCount", "memorySize", "diskSize"} {
		annotation := map[string]string{"cpuCount": "org.trycua.lume.cpu-count", "memorySize": "org.trycua.lume.memory-size", "diskSize": "org.trycua.lume.disk-size"}[key]
		value, err := strconv.ParseFloat(m.Annotations[annotation], 64)
		if err != nil {
			return computer.ErrIntegrity
		}
		expected[key] = value
	}
	expected["display"] = m.Annotations["org.trycua.lume.display"]
	expected["networkMode"] = "nat"
	if !reflect.DeepEqual(actual, expected) {
		return computer.ErrIntegrity
	}
	return nil
}

// Verify the actual uncompressed unit against the pinned OCI manifest, not the
// mutable tag Lume uses to download it. No unverified disk is ever booted.
func verifyImageFiles(ctx context.Context, root string, m imageManifest) error {
	if err := os.Chmod(root, 0700); err != nil {
		return err
	}
	if m.Config.Annotations["org.opencontainers.image.title"] != "config.json" {
		return computer.ErrIntegrity
	}
	if err := verifyImageConfig(root, m); err != nil {
		return err
	}
	var offset int64
	nvram := false
	for _, layer := range m.Layers {
		name := layer.Annotations["org.opencontainers.image.title"]
		if name == "nvram.bin" {
			if nvram {
				return computer.ErrIntegrity
			}
			nvram = true
			if err := verifyImageRange(ctx, root, name, 0, layer.Size, layer.Digest); err != nil {
				return err
			}
			continue
		}
		if !strings.HasPrefix(name, "disk.img.part.") {
			return computer.ErrIntegrity
		}
		at, err := strconv.ParseInt(layer.Annotations["org.trycua.lume.part.offset"], 10, 64)
		if err != nil || at != offset {
			return computer.ErrIntegrity
		}
		size, err := strconv.ParseInt(layer.Annotations["org.trycua.lume.content.uncompressed-size"], 10, 64)
		if err != nil || size <= 0 || size > 536870912 {
			return computer.ErrIntegrity
		}
		if err = verifyImageRange(ctx, root, "disk.img", offset, size, layer.Annotations["org.trycua.lume.content.uncompressed-digest"]); err != nil {
			return err
		}
		offset += size
	}
	info, err := os.Lstat(filepath.Join(root, "disk.img"))
	if err != nil || !info.Mode().IsRegular() || !nvram || offset == 0 || info.Size() != offset {
		return computer.ErrIntegrity
	}
	return nil
}

func verifyImageRange(ctx context.Context, root, name string, offset, size int64, digest string) error {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != 71 || size < 0 {
		return computer.ErrIntegrity
	}
	f, err := plainFile(filepath.Join(root, name))
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	r := io.NewSectionReader(f, offset, size)
	buf := make([]byte, 1<<20)
	var n int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		read, err := r.Read(buf)
		if read > 0 {
			_, _ = hash.Write(buf[:read])
			n += int64(read)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return computer.ErrIntegrity
		}
	}
	if n != size || hex.EncodeToString(hash.Sum(nil)) != strings.TrimPrefix(digest, "sha256:") {
		return computer.ErrIntegrity
	}
	return nil
}
