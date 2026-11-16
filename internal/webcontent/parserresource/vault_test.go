package parserresource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/webcontent/parserbundle"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var testNode string
var testLauncher string
var testModule []byte

func TestMain(m *testing.M) {
	if handled, err := Exec(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	var err error
	testNode, err = exec.LookPath("node")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testNode, err = filepath.EvalSymlinks(testNode)
	if err != nil {
		os.Exit(1)
	}
	directory, err := os.MkdirTemp("", "steward-parser-tests-")
	if err != nil {
		os.Exit(1)
	}
	source, _ := filepath.Abs("../../../internal/plugin/webextract/extract.mjs")
	dependencies, _ := filepath.Abs("../../../runtime/javascript/node_modules")
	testModule, err = parserbundle.Bundle(context.Background(), source, dependencies)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.RemoveAll(directory)
		os.Exit(1)
	}
	testLauncher, _ = os.Executable()
	if raceBuild {
		testLauncher = filepath.Join(directory, "carrier")
		command := exec.Command("go", "test", "-c", "-race=false", "-o", testLauncher, ".")
		if output, err := command.CombinedOutput(); err != nil {
			fmt.Fprintln(os.Stderr, string(output), err)
			os.RemoveAll(directory)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(directory)
	os.Exit(code)
}
func capture(t *testing.T, v Vault, module []byte) Revision {
	t.Helper()
	r, err := v.Capture(context.Background(), testNode, module)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func native(t *testing.T, input Request) Response {
	t.Helper()
	raw, _ := json.Marshal(input)
	command := exec.Command(testNode, "../../../runtime/javascript/src/web-extract.mjs")
	command.Stdin = bytes.NewReader(raw)
	result, err := command.Output()
	var response Response
	if err != nil || json.Unmarshal(result, &response) != nil || response.Error != "" {
		t.Fatal(string(result), err)
	}
	return response
}
func pdf() []byte {
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 1 /Kids [3 0 R] >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"}
	stream := "BT /F1 12 Tf 20 100 Td (Original sealed PDF evidence.) Tj ET"
	objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	var result strings.Builder
	result.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, result.Len())
		fmt.Fprintf(&result, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	start := result.Len()
	result.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&result, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&result, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", start)
	return []byte(result.String())
}

func TestWebAccessParserResourceActualHTMLPDFMatchOriginalDependencyBytes(t *testing.T) {
	v := Vault{Root: t.TempDir()}
	revision := capture(t, v, testModule)
	html := `<html><head><title>Original 中文 title</title></head><body><nav>noise</nav><main><h1>Evidence</h1>` + strings.Repeat(`<p>Independent original source evidence is kept.</p>`, 60) + `<a href="/reference">source</a><script>bad()</script></main></body></html>`
	for _, input := range []Request{{URL: "https://example.com/article", MediaType: "text/html", Data: []byte(html)}, {URL: "https://example.com/file.pdf", MediaType: "application/pdf", Data: pdf()}} {
		want := native(t, input)
		got, err := (Process{Vault: v, Launcher: testLauncher}).Parse(context.Background(), revision.Digest(), input)
		if err != nil || got.Title != want.Title || got.Text != want.Text || got.Error != "" {
			t.Fatalf("%s: got %#v want %#v err %v", input.MediaType, got, want, err)
		}
	}
}

func TestWebAccessParserResourceRetainsActualCommandAndModuleAcrossReplacement(t *testing.T) {
	ctx := context.Background()
	v := Vault{Root: t.TempDir()}
	source := filepath.Join(t.TempDir(), "original-node")
	in, err := os.Open(testNode)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		in.Close()
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	in.Close()
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	module := []byte(`process.stdout.write(JSON.stringify({title:"first",text:"original method"}));`)
	original, err := v.Capture(ctx, source, module)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(source, []byte("replacement command"), 0700); err != nil {
		t.Fatal(err)
	}
	copy(module, bytes.Repeat([]byte("x"), len(module)))
	newer := capture(t, v, []byte(`process.stdout.write(JSON.stringify({title:"second",text:"independently changed method"}));`))
	for _, item := range []struct {
		revision    Revision
		title, text string
	}{{original, "first", "original method"}, {newer, "second", "independently changed method"}, {original, "first", "original method"}} {
		got, err := (Process{Vault: v, Launcher: testLauncher}).Parse(ctx, item.revision.Digest(), Request{MediaType: "text/html", Data: []byte("original input")})
		if err != nil || got.Title != item.title || got.Text != item.text {
			t.Fatal(got, err)
		}
	}
	a, err := os.Stat(filepath.Join(v.Root, "revisions", name(original.Digest()), "node"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(filepath.Join(v.Root, "revisions", name(newer.Digest()), "node"))
	if err != nil || !os.SameFile(a, b) {
		t.Fatal("immutable interpreter was duplicated instead of retained", err)
	}
}

func TestWebAccessParserResourceRejectsCorruptionPathsAndUnboundedCapture(t *testing.T) {
	ctx := context.Background()
	fake := filepath.Join(t.TempDir(), "command")
	if err := os.WriteFile(fake, []byte("bounded fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"module", "engine", "metadata", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			v := Vault{Root: t.TempDir()}
			revision, err := v.Capture(ctx, fake, []byte("original module"))
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(v.Root, "revisions", name(revision.Digest()))
			target := filepath.Join(root, "parser.mjs")
			switch mode {
			case "engine":
				target = filepath.Join(root, "node")
			case "metadata":
				target = filepath.Join(root, "revision.json")
			}
			if mode == "symlink" {
				if err = os.Remove(target); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(fake, target)
			} else {
				if err = os.Chmod(target, 0600); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(target, []byte("tampered"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = v.Load(ctx, revision.Digest()); !errors.Is(err, ErrChanged) {
				t.Fatal(mode, err)
			}
		})
	}
	v := Vault{Root: t.TempDir()}
	for _, id := range []string{"../escape", "sha256:" + strings.Repeat("g", 64), "sha256:short"} {
		if _, err := v.Load(ctx, id); !errors.Is(err, ErrInvalid) {
			t.Fatal(id, err)
		}
	}
	if _, err := v.Capture(ctx, fake, make([]byte, MaxModuleBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	huge := filepath.Join(t.TempDir(), "large")
	f, err := os.OpenFile(huge, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(MaxEngineBytes + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.Capture(ctx, huge, []byte("module")); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = v.Capture(canceled, fake, []byte("module")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestWebAccessParserResourceOSBoundaryDeniesAmbientFSNetworkForkAndEnvironment(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(marker, []byte("PRIVATE_HOST_BYTES"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STEWARD_PRIVATE_PARSER_TEST", "PRIVATE_ENV")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.Write([]byte("NETWORK")) }))
	defer server.Close()
	module := []byte(fmt.Sprintf(`import fs from 'node:fs';import http from 'node:http';import child from 'node:child_process';const checks={};try{fs.readFileSync(%q);checks.read='leaked'}catch{checks.read='blocked'};try{fs.writeFileSync(%q,'leaked');checks.write='leaked'}catch{checks.write='blocked'};try{child.execFileSync('/usr/bin/true');checks.fork='allowed'}catch{checks.fork='blocked'};checks.env=process.env.STEWARD_PRIVATE_PARSER_TEST?'leaked':'blocked';checks.network=await new Promise(resolve=>{const request=http.get(%q,()=>resolve('allowed'));request.on('error',()=>resolve('blocked'))});process.stdout.write(JSON.stringify({title:'checks',text:JSON.stringify(checks)}));`, marker, marker+".new", server.URL))
	v := Vault{Root: t.TempDir()}
	revision := capture(t, v, module)
	got, err := (Process{Vault: v, Launcher: testLauncher}).Parse(context.Background(), revision.Digest(), Request{MediaType: "text/html"})
	if err != nil {
		t.Fatal(err)
	}
	var checks map[string]string
	if json.Unmarshal([]byte(got.Text), &checks) != nil || len(checks) != 5 {
		t.Fatal(got)
	}
	for key, value := range checks {
		if value != "blocked" {
			t.Fatal(key, value)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("candidate reached network")
	}
	if _, err = os.Stat(marker + ".new"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestWebAccessParserResourceOriginalEightMiBInputLargeOutputAndCancellation(t *testing.T) {
	v := Vault{Root: t.TempDir()}
	revision := capture(t, v, []byte(`let raw='';for await(const chunk of process.stdin)raw+=chunk;const q=JSON.parse(raw);if(Buffer.from(q.data,'base64').length!==8*1024*1024)throw Error('lost bytes');process.stdout.write(JSON.stringify({title:'full input',text:'x'.repeat(3*1024*1024)}));`))
	got, err := (Process{Vault: v, Launcher: testLauncher}).Parse(context.Background(), revision.Digest(), Request{MediaType: "text/html", Data: make([]byte, 8<<20)})
	if err != nil || got.Title != "full input" || len(got.Text) != 3<<20 {
		t.Fatal(len(got.Text), err)
	}
	loop := capture(t, v, []byte(`while(true){}`))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	before := time.Now()
	if _, err = (Process{Vault: v, Launcher: testLauncher}).Parse(ctx, loop.Digest(), Request{MediaType: "text/html"}); !errors.Is(err, context.DeadlineExceeded) || time.Since(before) > 5*time.Second {
		t.Fatal(err, time.Since(before))
	}
	excess := capture(t, v, []byte(`process.stdout.write('x'.repeat(8*1024*1024+1));`))
	if _, err = (Process{Vault: v, Launcher: testLauncher}).Parse(context.Background(), excess.Digest(), Request{MediaType: "text/html"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
