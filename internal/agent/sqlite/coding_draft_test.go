package sqlite

import (
	"bytes"
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/codingdraft"
	"github.com/z-chenhao/assistant/internal/llm/memory"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Re-route the production adapter's fixed public origin to the local API peer.
type draftTestTransport struct{ server *httptest.Server }

func (t draftTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.github.com" || r.URL.Scheme != "https" {
		return nil, fmt.Errorf("unexpected destination")
	}
	dest, _ := url.Parse(t.server.URL)
	c := r.Clone(r.Context())
	c.URL.Host = dest.Host
	c.URL.Scheme = dest.Scheme
	return t.server.Client().Transport.RoundTrip(c)
}

type draftPeer struct {
	mu           sync.Mutex
	t            *testing.T
	head         string
	stored       map[string]any
	posts        []string
	reads        int
	loseResponse bool
	mismatched   bool
}

const draftBase = "1111111111111111111111111111111111111111"
const draftHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const emptyGitTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

func peerObject(kind string, data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d%c", kind, len(data), byte(0))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
func (p *draftPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer local-owner-fixture" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
		p.t.Errorf("missing owner authentication")
		http.Error(w, "unauthorized", 401)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, "/repos/example/project")
	if !ok {
		http.NotFound(w, r)
		return
	}
	var out any
	mode, branch := "100644", "review/candidate"
	blob := peerObject("blob", []byte("dirty source"))
	id, _ := hex.DecodeString(blob)
	tree := peerObject("tree", append([]byte("100644 file.txt\x00"), id...))
	if r.Method == "POST" {
		p.posts = append(p.posts, path)
	}
	switch r.Method + " " + path {
	case "GET ":
		out = map[string]any{"id": 42, "full_name": "example/project"}
	case "GET /git/ref/heads/main":
		out = map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": draftBase, "type": "commit"}}
	case "GET /git/ref/heads/review/candidate":
		if p.head == "" {
			http.NotFound(w, r)
			return
		}
		out = map[string]any{"ref": "refs/heads/" + branch, "object": map[string]string{"sha": p.head, "type": "commit"}}
	case "GET /git/commits/" + draftBase:
		out = map[string]any{"sha": draftBase, "tree": map[string]string{"sha": emptyGitTree}}
	case "GET /git/trees/" + emptyGitTree:
		if r.URL.Query().Get("recursive") != "1" {
			p.t.Error("read incomplete tree")
		}
		out = map[string]any{"sha": emptyGitTree, "tree": []any{}, "truncated": false}
	case "POST /git/blobs":
		var b struct{ Content, Encoding string }
		e := json.NewDecoder(r.Body).Decode(&b)
		data, err := base64.StdEncoding.DecodeString(b.Content)
		if e != nil || err != nil || string(data) != "dirty source" || b.Encoding != "base64" {
			p.t.Errorf("wrong Git blob")
		}
		out = map[string]string{"sha": peerObject("blob", data)}
	case "POST /git/trees":
		var v struct{ Tree []coding.DraftEntry }
		err := json.NewDecoder(r.Body).Decode(&v)
		if err != nil || len(v.Tree) != 1 || v.Tree[0] != (coding.DraftEntry{Path: "file.txt", Mode: mode, Type: "blob", SHA: blob}) {
			p.t.Errorf("wrong tree %+v", v.Tree)
		}
		out = map[string]string{"sha": tree}
	case "POST /git/commits":
		var v struct {
			Message, Tree string
			Parents       []string
		}
		err := json.NewDecoder(r.Body).Decode(&v)
		if err != nil || v.Tree != tree || !slices.Equal(v.Parents, []string{draftBase}) || v.Message != "Owner reviewed change" {
			p.t.Error("commit lost approved tree, title or parent")
		}
		out = map[string]any{"sha": draftHead, "tree": map[string]string{"sha": tree}, "parents": []map[string]string{{"sha": draftBase}}}
	case "POST /git/refs":
		var v struct{ Ref, SHA string }
		err := json.NewDecoder(r.Body).Decode(&v)
		if err != nil || p.head != "" || v.Ref != "refs/heads/"+branch || v.SHA != draftHead {
			p.t.Error("branch creation lost exact approved identity")
		}
		p.head = v.SHA
		out = map[string]any{"ref": v.Ref, "object": map[string]string{"sha": p.head}}
	case "POST /pulls":
		var v struct {
			Head, Base, Title, Body string
			Draft                   bool
		}
		err := json.NewDecoder(r.Body).Decode(&v)
		if err != nil || p.head != draftHead || !v.Draft || v.Head != branch || v.Base != "main" || v.Title != "Owner reviewed change" || !strings.Contains(v.Body, "<!-- steward-draft:") {
			p.t.Errorf("incorrect draft request %+v", v)
		}
		repo := map[string]int{"id": 42}
		p.stored = map[string]any{"number": 7, "draft": v.Draft, "html_url": "https://github.com/example/project/pull/7", "body": v.Body, "head": map[string]any{"repo": repo, "ref": v.Head, "sha": p.head}, "base": map[string]any{"repo": repo, "ref": v.Base, "sha": draftBase}}
		if p.loseResponse {
			http.Error(w, "private upstream diagnostic", 502)
			return
		}
		out = p.stored
	case "GET /pulls":
		p.reads++
		q := r.URL.Query()
		if q.Get("head") != "example:"+branch || q.Get("state") != "all" || q.Get("base") != "main" {
			p.t.Error("reconciled wrong target")
		}
		if p.mismatched {
			bad := map[string]any{}
			for k, v := range p.stored {
				bad[k] = v
			}
			bad["body"] = "another publication"
			out = []any{bad}
		} else {
			out = []any{p.stored}
		}
	default:
		p.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (p *draftPeer) writes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.posts)
}
func (p *draftPeer) reconciliations() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reads
}
func (p *draftPeer) mismatch(on bool) { p.mu.Lock(); defer p.mu.Unlock(); p.mismatched = on }

func draftTestCLI(t *testing.T, s *Store, server *httptest.Server) func(string, string, ...string) (codingdraft.Receipt, error) {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "steward")
	if out, err := exec.Command("go", "test", "-c", "-o", binary, "../../../cmd/steward").CombinedOutput(); err != nil {
		t.Fatalf("build CLI test peer: %s: %v", out, err)
	}
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("version: 1\napp:\n  data_root: "+s.DataRoot()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return func(op, input string, args ...string) (codingdraft.Receipt, error) {
		t.Helper()
		v := codingdraft.Receipt{}
		argv := append([]string{"-config", cfg, "coding", op}, args...)
		cmd := exec.Command(binary, argv...)
		cmd.Env = append(os.Environ(), "STEWARD_DATA_ROOT="+s.DataRoot(), "STEWARD_TEST_DRAFT_LOOPBACK="+server.URL, "STEWARD_DRAFT_TEST_KEY=local-owner-fixture")
		cmd.Stdin = strings.NewReader(input)
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		out, err := cmd.Output()
		if err != nil {
			return v, fmt.Errorf("%w: %s", err, diagnostics.String())
		}
		if err := json.Unmarshal(out, &v); err != nil {
			return v, fmt.Errorf("CLI output %s: %w", out, err)
		}
		return v, nil
	}
}

func TestCodingDraftOwnerApprovalAndUnknownReconciliation(t *testing.T) {
	ctx := context.Background()
	s, r, a, model, _ := codingCheckFixture(t)
	h := installCodingPlugin(t, s, r)
	model.respond = codingCheckProvider(t, "test \"$(cat file.txt)\" = 'dirty source'", nil)
	mustSubmit(t, a, "draft-publication", "Verify this candidate for review")
	mustDrain(t, a)
	var ref, dir string
	if err := s.db.QueryRow("SELECT t.task_ref,e.directory FROM task t JOIN task_code_environment e USING(task_id)").Scan(&ref, &dir); err != nil {
		t.Fatal(err)
	}
	peer := &draftPeer{t: t, loseResponse: true}
	server := httptest.NewServer(peer)
	defer server.Close()
	secrets := memory.New()
	target := codingdraft.Target{Repository: "example/project", Base: "main", Branch: "review/candidate", Title: "Owner reviewed change", CredentialRef: "env:STEWARD_DRAFT_TEST_KEY"}
	if err := secrets.Put(ctx, target.CredentialRef, []byte("local-owner-fixture")); err != nil {
		t.Fatal(err)
	}
	remote := codingdraft.GitHub{Secrets: secrets, Transport: draftTestTransport{server}}
	if _, err := s.PrepareCodingDraft(ctx, remote, ref, target); err == nil {
		t.Fatal("waiting check authorized publication")
	}
	approveCodingCheck(t, s, r)
	mustDrain(t, a)
	cli := draftTestCLI(t, s, server)
	targetJSON, _ := json.Marshal(target)
	plan, err := cli("draft-plan", string(targetJSON), ref)
	if err != nil || plan.Status != "planned" || len(plan.Plan.Changes) != 1 || plan.Plan.Changes[0].Path != "file.txt" || plan.Plan.Candidate != checkReceipt(t, s).CandidateSHA256 || len(peer.writes()) != 0 {
		t.Fatalf("plan %+v: %v", plan, err)
	}
	staleTarget := target
	staleTarget.Title = "Second approved selection"
	stale, err := s.PrepareCodingDraft(ctx, remote, ref, staleTarget)
	if err != nil || stale.Approval == plan.Approval {
		t.Fatal("target did not bind", stale, err)
	}
	first, err := h.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := selectCodingVersion(t, h, first.Revision, "1.1.0")
	if _, err = cli("draft-approve", "", ref, plan.Approval); err == nil || len(peer.writes()) != 0 {
		t.Fatal("changed plugin graph reused old approval", err)
	}
	activateDelegation(t, h, "draft-rollback", second, first.Revision, "coding-owner-fixture")
	unknown, err := cli("draft-approve", "", ref, plan.Approval)
	if err != nil || unknown.Status != "unknown" || unknown.Head != draftHead || unknown.Number != 0 || !slices.Equal(peer.writes(), []string{"/git/blobs", "/git/trees", "/git/commits", "/git/refs", "/pulls"}) {
		t.Fatalf("unknown %+v writes %v: %v", unknown, peer.writes(), err)
	}
	if _, err := s.PrepareCodingDraft(ctx, remote, ref, target); err == nil {
		t.Fatal("existing remote branch accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("changed after planning"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveCodingDraft(ctx, remote, ref, stale.Approval); err == nil {
		t.Fatal("stale candidate accepted")
	}
	reopened, err := Open(ctx, s.DataRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	duplicate, err := reopened.ApproveCodingDraft(ctx, remote, ref, plan.Approval)
	if err != nil || duplicate.Status != "unknown" || len(peer.writes()) != 5 {
		t.Fatal("uncertain publication replayed", duplicate, err)
	}
	peer.mismatch(true)
	wrong, err := reopened.ReconcileCodingDraft(ctx, remote, ref, plan.Approval)
	if err != nil || wrong.Status != "unknown" {
		t.Fatal("accepted unrelated PR", wrong, err)
	}
	peer.mismatch(false)
	published, err := cli("draft-reconcile", "", ref, plan.Approval)
	if err != nil || published.Status != "published" || published.Number != 7 || published.URL != "https://github.com/example/project/pull/7" || len(peer.writes()) != 5 || peer.reconciliations() != 2 {
		t.Fatal("lost original or repeated remote effect", published, peer.writes(), peer.reconciliations(), err)
	}
	saved, err := cli("draft-status", "", ref, plan.Approval)
	if err != nil || saved.URL != published.URL {
		t.Fatal("new CLI process lost original remote receipt", saved, err)
	}
	again, err := s.ApproveCodingDraft(ctx, remote, ref, plan.Approval)
	if err != nil || again.Number != 7 || len(peer.writes()) != 5 {
		t.Fatal("published approval changed", again, err)
	}
	if _, err := h.Store.Deactivate(ctx, "disable-draft", first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := cli("draft-plan", string(targetJSON), ref); err == nil || len(peer.writes()) != 5 {
		t.Fatal("inactive method dispatched", err)
	}
	owner := idAgent(t, s)
	if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, owner) }); err != nil {
		t.Fatal(err)
	}
	if _, err := cli("draft-status", "", ref, plan.Approval); err == nil {
		t.Fatal("forgotten task exposed its publication receipt")
	}
}
