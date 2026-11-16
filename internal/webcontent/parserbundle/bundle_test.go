package parserbundle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func ownerSource(t *testing.T, source string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	entry := filepath.Join(root, "app", "src", "owner.mjs")
	dependencies := filepath.Join(root, "node_modules")
	for name, data := range map[string]string{
		entry: source,
		filepath.Join(dependencies, "fixture-lib", "package.json"): `{"name":"fixture-lib","version":"1.2.3","type":"module","exports":"./index.mjs"}`,
		filepath.Join(dependencies, "fixture-lib", "index.mjs"):    `export const word = 'original dependency';`,
		filepath.Join(dependencies, "fixture-lib", "LICENSE"):      "Complete library notice\nincluding a */ delimiter.\nAll original terms retained.\n",
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return entry, dependencies, root
}

func execute(t *testing.T, path string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, path)
	command.Dir = t.TempDir()
	command.Env = []string{"NODE_OPTIONS=--no-global-search-paths", "PATH=/usr/bin:/bin"}
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	return string(out)
}

func TestWebAccessParserBundleDoesNotExecuteSourceAndRetainsCompleteNotices(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	source, deps, _ := ownerSource(t, `import fs from 'node:fs';import {word} from 'fixture-lib';fs.writeFileSync(`+strconv.Quote(marker)+`,word);throw Error('owner source executed');`)
	module, err := Bundle(context.Background(), source, deps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("compiler executed owner source", err)
	}
	for _, text := range []string{"fixture-lib@1.2.3", "Complete library notice", "including a * / delimiter.", "All original terms retained."} {
		if !strings.Contains(string(module), text) {
			t.Fatal("notice omitted", text)
		}
	}
	if strings.Contains(string(module), source) {
		t.Fatal("source location leaked through compiler comments")
	}
}

func TestWebAccessParserBundleRetainsActualOwnerImportsAfterSourceReplacement(t *testing.T) {
	source, deps, _ := ownerSource(t, `import {word} from 'fixture-lib';process.stdout.write(JSON.stringify({title:'owner',text:word}));`)
	want := execute(t, source)
	module, err := Bundle(context.Background(), source, deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("throw Error('replaced source')"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deps, "fixture-lib", "index.mjs"), []byte("throw Error('replaced dependency')"), 0600); err != nil {
		t.Fatal(err)
	}
	sealed := filepath.Join(t.TempDir(), "sealed.mjs")
	if err := os.WriteFile(sealed, module, 0600); err != nil {
		t.Fatal(err)
	}
	if got := execute(t, sealed); got != want {
		t.Fatal(got, want)
	}
}

func TestWebAccessParserBundleRejectsEscapesUnboundedInputAndNetworkResolution(t *testing.T) {
	source, deps, _ := ownerSource(t, `export const value=1;`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Bundle(ctx, source, deps); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Bundle(context.Background(), "relative.mjs", deps); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte(strings.Repeat("x", MaxModuleBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Bundle(context.Background(), source, deps); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private.mjs")
	if err := os.WriteFile(outside, []byte(`export default 'PRIVATE_OUTSIDE_ROOT';`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("import value from "+strconv.Quote(outside)+";console.log(value);"), 0600); err != nil {
		t.Fatal(err)
	}
	if module, err := Bundle(context.Background(), source, deps); !errors.Is(err, ErrInvalid) || len(module) != 0 {
		t.Fatal(string(module), err)
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	if err := os.WriteFile(source, []byte("import "+strconv.Quote(server.URL+"/module.mjs")+";"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Bundle(context.Background(), source, deps); !errors.Is(err, ErrInvalid) || requests.Load() != 0 {
		t.Fatal(requests.Load(), err)
	}
}

func TestWebAccessParserBundleRejectsSpecialFilesWithoutBlocking(t *testing.T) {
	source, deps, _ := ownerSource(t, `export const value=1;`)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(source, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Bundle(context.Background(), source, deps); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestWebAccessParserBundleKeepsCanvasAbsentAndRejectsEscapingNotice(t *testing.T) {
	source, deps, _ := ownerSource(t, `let loaded=false;try{require('canvas');loaded=true}catch{};process.stdout.write(String(loaded));`)
	canvas := filepath.Join(deps, "canvas")
	if err := os.MkdirAll(canvas, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canvas, "package.json"), []byte(`{"name":"canvas","main":"index.js"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canvas, "index.js"), []byte(`module.exports={available:true};`), 0600); err != nil {
		t.Fatal(err)
	}
	module, err := Bundle(context.Background(), source, deps)
	if err != nil {
		t.Fatal(err)
	}
	sealed := filepath.Join(t.TempDir(), "sealed.mjs")
	if err := os.WriteFile(sealed, module, 0600); err != nil {
		t.Fatal(err)
	}
	if got := execute(t, sealed); got != "false" {
		t.Fatal("ambient optional canvas was bundled", got)
	}
	if err := os.WriteFile(source, []byte(`import {word} from 'fixture-lib';console.log(word);`), 0600); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(private, []byte("PRIVATE_NOTICE_ESCAPE"), 0600); err != nil {
		t.Fatal(err)
	}
	license := filepath.Join(deps, "fixture-lib", "LICENSE")
	if err := os.Remove(license); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, license); err != nil {
		t.Fatal(err)
	}
	if module, err := Bundle(context.Background(), source, deps); !errors.Is(err, ErrInvalid) || len(module) != 0 {
		t.Fatal(string(module), err)
	}
}
