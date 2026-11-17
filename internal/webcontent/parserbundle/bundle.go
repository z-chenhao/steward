// Package parserbundle seals owner-selected parser source and local dependencies.
// It is a build/control mechanism, never a candidate SDK or model operation.
package parserbundle

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/evanw/esbuild/pkg/api"
)

const MaxModuleBytes = 16 << 20
const maxSourceBytes = 128 << 20
const maxSourceFiles = 4096

var ErrInvalid = errors.New("web parser module cannot be sealed")

// Bundle parses code without executing it. Source imports stay within the
// original extractor's trusted parent scope and the explicit dependency tree.
// There is no caller-supplied compiler plugin, transform or external resolver.
func Bundle(ctx context.Context, source, dependencies string) ([]byte, error) {
	if !filepath.IsAbs(source) || !filepath.IsAbs(dependencies) {
		return nil, ErrInvalid
	}
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, ErrInvalid
	}
	dependencies, err = filepath.EvalSymlinks(dependencies)
	if err != nil {
		return nil, ErrInvalid
	}
	info, err := os.Stat(dependencies)
	if err != nil || !info.IsDir() {
		return nil, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	root := filepath.Dir(filepath.Dir(source)) // Existing extractor config scope.
	roots := []string{root, dependencies}
	var mu sync.Mutex
	files := map[string]bool{}
	bytes := 0
	build, setupErr := api.Context(api.BuildOptions{
		EntryPoints: []string{source}, AbsWorkingDir: root, NodePaths: []string{dependencies},
		Bundle: true, Platform: api.PlatformNode, Format: api.FormatESModule,
		External: []string{"canvas"},
		Write:    false, Metafile: true, MinifyWhitespace: true, LegalComments: api.LegalCommentsEndOfFile,
		LogLevel: api.LogLevelSilent, TsconfigRaw: `{}`,
		Banner: map[string]string{"js": `import { createRequire as stewardRequire } from "node:module"; const require = stewardRequire(import.meta.url);`},
		Plugins: []api.Plugin{{Name: "bounded-parser-input", Setup: func(b api.PluginBuild) {
			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "file"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				if err := ctx.Err(); err != nil {
					return api.OnLoadResult{}, err
				}
				path, err := filepath.EvalSymlinks(args.Path)
				if err != nil || !withinRoots(roots, path) {
					return api.OnLoadResult{}, ErrInvalid
				}
				raw, err := readScoped(roots, path, MaxModuleBytes)
				if err != nil {
					return api.OnLoadResult{}, err
				}
				mu.Lock()
				if !files[path] {
					files[path] = true
					bytes += len(raw)
				}
				over := bytes > maxSourceBytes || len(files) > maxSourceFiles
				mu.Unlock()
				if over {
					return api.OnLoadResult{}, ErrInvalid
				}
				contents := string(raw)
				return api.OnLoadResult{Contents: &contents, ResolveDir: filepath.Dir(path), Loader: api.LoaderDefault}, nil
			})
		}}},
	})
	if setupErr != nil {
		return nil, ErrInvalid
	}
	defer build.Dispose()
	finished := make(chan struct{})
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			build.Cancel()
		case <-finished:
		}
	}()
	result := build.Rebuild()
	close(finished)
	<-joined
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(result.Errors) != 0 || len(result.OutputFiles) != 1 || len(result.OutputFiles[0].Contents) > MaxModuleBytes {
		return nil, ErrInvalid
	}
	var metadata struct {
		Outputs map[string]struct {
			Imports []struct {
				Path     string `json:"path"`
				External bool   `json:"external"`
			} `json:"imports"`
		} `json:"outputs"`
	}
	if json.Unmarshal([]byte(result.Metafile), &metadata) != nil {
		return nil, ErrInvalid
	}
	for _, output := range metadata.Outputs {
		for _, dependency := range output.Imports {
			if dependency.External && dependency.Path != "canvas" && !nodeBuiltin(dependency.Path) {
				return nil, ErrInvalid
			}
		}
	}
	// The optional canvas probe has always been absent from the text extractor;
	// an installed external canvas must not silently become an ambient resource.
	notices, err := dependencyNotices(ctx, roots, files)
	if err != nil {
		return nil, err
	}
	module := append(result.OutputFiles[0].Contents, []byte("\n/* Sealed dependency notices\n"+strings.ReplaceAll(notices, "*/", "* /")+"\n*/\n")...)
	if len(module) > MaxModuleBytes {
		return nil, ErrInvalid
	}
	return module, nil
}

func withinRoots(roots []string, path string) bool {
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
func readScoped(roots []string, path string, limit int) ([]byte, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !withinRoots(roots, resolved) {
		return nil, ErrInvalid
	}
	for _, root := range roots {
		if !withinRoots([]string{root}, resolved) {
			continue
		}
		directory, err := os.OpenRoot(root)
		if err != nil {
			return nil, ErrInvalid
		}
		relative, err := filepath.Rel(root, resolved)
		if err != nil {
			directory.Close()
			return nil, ErrInvalid
		}
		// Root prevents symlink replacement from escaping the original scope.
		// NONBLOCK prevents a replaced file/FIFO from hanging before Stat.
		file, err := directory.OpenFile(relative, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		directory.Close()
		if err != nil {
			return nil, ErrInvalid
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > int64(limit) {
			return nil, ErrInvalid
		}
		raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
		if err != nil || len(raw) > limit {
			return nil, ErrInvalid
		}
		return raw, nil
	}
	return nil, ErrInvalid
}

func dependencyNotices(ctx context.Context, roots []string, files map[string]bool) (string, error) {
	packages := map[string]string{}
	for file := range files {
		for directory := filepath.Dir(file); withinRoots(roots, directory); directory = filepath.Dir(directory) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			metadataPath := filepath.Join(directory, "package.json")
			_, statErr := os.Lstat(metadataPath)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return "", ErrInvalid
			}
			if statErr == nil {
				raw, err := readScoped(roots, metadataPath, 256<<10)
				if err != nil {
					return "", err
				}
				var metadata struct{ Name, Version string }
				if json.Unmarshal(raw, &metadata) != nil {
					return "", ErrInvalid
				}
				if strings.Contains(filepath.ToSlash(directory), "/node_modules/") {
					packages[directory] = metadata.Name + "@" + metadata.Version
				}
				break
			}
			if filepath.Dir(directory) == directory {
				break
			}
		}
	}
	directories := make([]string, 0, len(packages))
	for directory := range packages {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	var notices strings.Builder
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return "", ErrInvalid
		}
		for _, entry := range entries {
			name := strings.ToLower(entry.Name())
			if !strings.HasPrefix(name, "license") && !strings.HasPrefix(name, "licence") && !strings.HasPrefix(name, "notice") && !strings.HasPrefix(name, "copying") {
				continue
			}
			if entry.IsDir() {
				continue
			}
			raw, err := readScoped(roots, filepath.Join(directory, entry.Name()), 256<<10)
			if err != nil {
				return "", err
			}
			notices.WriteString(packages[directory] + " — " + entry.Name() + "\n")
			notices.Write(raw)
			notices.WriteString("\n\n")
			if notices.Len() > MaxModuleBytes {
				return "", ErrInvalid
			}
		}
	}
	return notices.String(), nil
}

// Observed Node24.21.0 builtin names. node: references cannot resolve an ambient
// package; any unsupported builtin still fails in the isolated interpreter.
func nodeBuiltin(name string) bool {
	if strings.HasPrefix(name, "node:") {
		return true
	}
	for _, builtin := range strings.Fields(`_http_agent _http_client _http_common _http_incoming _http_outgoing _http_server _stream_duplex _stream_passthrough _stream_readable _stream_transform _stream_wrap _stream_writable _tls_common _tls_wrap assert assert/strict async_hooks buffer child_process cluster console constants crypto dgram diagnostics_channel dns dns/promises domain events fs fs/promises http http2 https inspector inspector/promises module net os path path/posix path/win32 perf_hooks process punycode querystring readline readline/promises repl stream stream/consumers stream/promises stream/web string_decoder sys timers timers/promises tls trace_events tty url util util/types v8 vm wasi worker_threads zlib`) {
		if name == builtin {
			return true
		}
	}
	return false
}
