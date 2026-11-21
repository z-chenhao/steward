package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/coding"
	"github.com/z-chenhao/assistant/internal/codingdraft"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/interaction/feishu"
	"github.com/z-chenhao/assistant/internal/llm"
	secretmemory "github.com/z-chenhao/assistant/internal/llm/memory"
	"github.com/z-chenhao/assistant/internal/observer/github"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/task"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type repositoryReviewCallback struct {
	*Store
	*Runs
	owner artifact.Scope
}

func (c repositoryReviewCallback) DescribeArtifact(ctx context.Context, ref string) (artifact.Descriptor, error) {
	return c.Artifacts().Describe(ctx, c.owner, ref)
}

// One graph freezes the workspace implementation and the PR reader together.
func installRepositoryReviewPlugins(t *testing.T, s *Store, r *Runs) {
	t.Helper()
	ctx := context.Background()
	host := installCodingPlugin(t, s, r)
	active, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := host.Install(ctx, githubVersionPackage(t, "1.0.2"))
	if err != nil {
		t.Fatal(err)
	}
	joinJourneyPlugin(t, host, active.Revision, "coding-owner-fixture", "repository-review-owner", "github-pr-followup", pkg.Digest, githubOwnerPolicy("1.0.2"))
}

// Both replaceable features run under one owner-verified graph.
func joinJourneyPlugin(t *testing.T, host rsi.Host, revision, basePolicy, policyID, instance, digest string, additional rsi.Policy) {
	t.Helper()
	ctx := context.Background()
	graph, err := host.Store.Graph(ctx, revision)
	if err != nil {
		t.Fatal(err)
	}
	graph.Graph.Bindings = append(graph.Graph.Bindings, rsiplugin.Binding{InstanceID: instance, PackageDigest: digest, Config: json.RawMessage(`{}`), StateNamespace: instance, StateVersion: 1})
	combined, err := host.Prepare(ctx, graph.Graph)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := host.Store.Policy(ctx, basePolicy)
	if err != nil {
		t.Fatal(err)
	}
	policy.ID = policyID
	policy.Cases = append(policy.Cases, additional.Cases...)
	policy.AllowedContracts = append(policy.AllowedContracts, additional.AllowedContracts...)
	policy.AllowedRequests = append(policy.AllowedRequests, additional.AllowedRequests...)
	acceptArtifactOwnerGraph(t, host, combined.Revision, policy)
}

func TestBoundFeishuRepositoryDraftReviewJourney(t *testing.T) {
	ctx := context.Background()
	s, r, a, model, workspace := codingCheckFixture(t)
	installRepositoryReviewPlugins(t, s, r)
	source := filepath.Join(workspace.RepositorySource, "file.txt")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	binding, err := s.BindChannel(ctx, "feishu", "tenant:app:owner", "bound-chat", true, 0)
	if err != nil {
		t.Fatal(err)
	}

	var sentMu sync.Mutex
	var sent []string
	const topic = "om_repository_work"
	const other = "om_unrelated"
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil || req.Method != "POST" || req.Header.Get("Authorization") != "Bearer local-chat-token" {
			t.Error("unexpected Feishu delivery", err, req.Method)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sentMu.Lock()
		defer sentMu.Unlock()
		sent = append(sent, req.URL.Path+" "+string(body))
		fmt.Fprintf(w, `{"code":0,"data":{"message_id":"om_reply_%d"}}`, len(sent))
	}))
	defer peer.Close()
	keys := secretmemory.New()
	for ref, value := range map[string]string{"secret:access": "local-chat-token", "secret:encrypt": "local-signature-key", "secret:verify": "local-verifier", "env:STEWARD_DRAFT_TEST_KEY": "local-owner-fixture"} {
		if err := keys.Put(ctx, ref, []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	client := feishu.Client{Secrets: keys, Config: feishu.Config{TenantKey: "tenant", AppID: "app", AccessTokenRef: "secret:access", EncryptKeyRef: "secret:encrypt", VerificationTokenRef: "secret:verify", APIBase: peer.URL}}
	handler := feishu.Handler{Client: client, Core: repositoryReviewCallback{s, r, artifactOwner(t, s)}}
	callback := func(id, message string) {
		t.Helper()
		content, _ := json.Marshal(map[string]string{"text": message})
		payload := fmt.Sprintf(`{"schema":"2.0","header":{"event_id":"event-%s","app_id":"app","tenant_key":"tenant","token":"local-verifier","event_type":"im.message.receive_v1"},"event":{"sender":{"sender_type":"user","sender_id":{"open_id":"owner"}},"message":{"chat_id":"bound-chat","chat_type":"p2p","message_id":%q,"message_type":"text","content":%q}}}`, id, id, string(content))
		stamp := fmt.Sprint(time.Now().Unix())
		req := httptest.NewRequest(http.MethodPost, "/feishu/callback", strings.NewReader(payload))
		req.Header.Set("X-Lark-Request-Timestamp", stamp)
		req.Header.Set("X-Lark-Request-Nonce", "journey")
		req.Header.Set("X-Lark-Signature", fmt.Sprintf("%x", sha256.Sum256([]byte(stamp+"journeylocal-signature-key"+payload))))
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"accepted":true`) {
			t.Fatalf("Feishu ingress %d: %s", res.Code, res.Body.String())
		}
	}
	drain := func() []string {
		t.Helper()
		mustDrain(t, a)
		out := delivery.Service{Store: s.Deliveries(), History: r.history, Adapter: feishu.Adapter{Client: client, Store: s}, Channel: binding.Channel, Destination: binding.Destination, Policy: delivery.DefaultPolicy()}
		if err := out.Drain(ctx); err != nil {
			t.Fatal(err)
		}
		sentMu.Lock()
		defer sentMu.Unlock()
		return append([]string(nil), sent...)
	}
	const originalCheck = `test "$(cat file.txt)" = 'dirty source' && printf original_check`
	const reviewCheck = `test "$(cat file.txt)" = 'dirty source' && printf review_check`
	const feedback = "Check file.txt again against the approved draft."
	check := codingCheckProvider(t, originalCheck, nil)
	var ref string
	model.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			last := req.Messages[len(req.Messages)-1]
			if last.Role != "tool" {
				return calls("builtin__write", `{"path":"file.txt","content":"dirty source"}`), nil
			}
			previous := req.Messages[len(req.Messages)-2]
			if len(previous.ToolCalls) == 1 && previous.ToolCalls[0].Name == "builtin__write" {
				return calls(coding.StatusTool, `{}`), nil
			}
			return check(req)
		}
		return check(req)
	}
	callback(topic, "Create file.txt in the repository and verify it for a draft PR.")
	callback(topic, "Create file.txt in the repository and verify it for a draft PR.")
	mustDrain(t, a)
	approvePolicy(t, s, r)
	mustDrain(t, a)
	approveCodingCheck(t, s, r)
	drain()
	initial := checkReceipt(t, s)
	if initial.Outcome != "passed" || initial.Output != "original_check" {
		t.Fatal("initial verification", initial)
	}
	if err := s.db.QueryRow(`SELECT task_ref FROM task`).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("new file escaped the task checkout", err)
	}

	draftAPI := &draftPeer{t: t}
	server := httptest.NewServer(draftAPI)
	defer server.Close()
	target := codingdraft.Target{Repository: "example/project", Base: "main", Branch: "review/candidate", Title: "Owner reviewed change", CredentialRef: "env:STEWARD_DRAFT_TEST_KEY"}
	remote := codingdraft.GitHub{Secrets: keys, Transport: draftTestTransport{server}}
	plan, err := s.PrepareCodingDraft(ctx, remote, ref, target)
	if err != nil || plan.Plan.Candidate != initial.CandidateSHA256 || len(draftAPI.writes()) != 0 {
		t.Fatal("draft plan", plan, err)
	}
	published, err := s.ApproveCodingDraft(ctx, remote, ref, plan.Approval)
	if err != nil || published.Status != "published" || published.Number != 7 || published.Head != draftHead {
		t.Fatal("draft publication", published, err)
	}

	model.respond = func(llm.GenerationRequest) (llm.GenerationResult, error) {
		return llm.GenerationResult{Text: "Separate conversation"}, nil
	}
	callback(other, "Hello from another topic.")
	drain()

	// Watch the published receipt's PR; the owner explicitly binds the original Task.
	cfg := githubConfig()
	cfg.Repository, cfg.PullNumber = published.Plan.Target.Repository, published.Number
	if err := s.ConfigureGitHub(ctx, &cfg); err != nil {
		t.Fatal(err)
	}
	var commentID atomic.Int64
	pullPath := fmt.Sprintf("/repos/%s/pulls/%d", cfg.Repository, cfg.PullNumber)
	watch := githubServicePeer(t, s, cfg, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/user":
			fmt.Fprint(w, `{"id":1,"login":"owner"}`)
		case pullPath:
			fmt.Fprintf(w, `{"number":%d,"head":{"sha":%q},"base":{"repo":{"id":42,"full_name":%q}}}`, published.Number, published.Head, cfg.Repository)
		case pullPath + "/comments":
			id := commentID.Load()
			if id == 0 {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprintf(w, `[{"id":%d,"body":%q,"updated_at":"2026-11-01T10:00:00Z","html_url":"%s#discussion_r%d","user":{"id":2,"login":"reviewer"}}]`, id, feedback, published.URL, id)
		case pullPath + "/reviews":
			fmt.Fprint(w, `[]`)
		case "/repos/" + cfg.Repository + "/commits/" + published.Head + "/check-runs":
			fmt.Fprint(w, `{"check_runs":[]}`)
		default:
			t.Error("unexpected watch endpoint", req.URL.Path)
			http.NotFound(w, req)
		}
	})
	poll := func() {
		t.Helper()
		if err := watch.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	poll()
	if err := s.BindGitHubTask(ctx, cfg, github.BindingRequest{TaskRef: ref, TaskEpoch: 1, Mode: "automatic", EventKind: "review_comment"}, true); err != nil {
		t.Fatal(err)
	}
	check = codingCheckProvider(t, reviewCheck, nil)
	wantResult := "Review check passed for " + initial.CandidateSHA256
	model.respond = func(req llm.GenerationRequest) (llm.GenerationResult, error) {
		if isRoot(req) {
			last := req.Messages[len(req.Messages)-1]
			if last.Role == "tool" {
				var receipt coding.CheckReceipt
				if json.Unmarshal([]byte(last.Text), &receipt) == nil && receipt.Outcome == "passed" && receipt.Output == "review_check" {
					return llm.GenerationResult{Text: "Review check passed for " + receipt.CandidateSHA256}, nil
				}
			}
			return check(req)
		}
		if isGitHubFollowup(req) && !githubToolReturned(req) {
			for _, m := range req.Messages {
				if m.Origin == "runtime" && strings.Contains(m.Text, feedback) {
					return calls(task.ManageName, fmt.Sprintf(`{"operation":"append","taskRef":%q,"instruction":%q}`, ref, feedback)), nil
				}
			}
			return llm.GenerationResult{}, errors.New("review never reached Main as external evidence")
		}
		if isFollowup(req) {
			for _, m := range req.Messages {
				if m.Origin == "runtime" && strings.Contains(m.Text, wantResult) {
					return llm.GenerationResult{Text: wantResult}, nil
				}
			}
			return llm.GenerationResult{}, errors.New("original Task report lacks verification receipt")
		}
		return llm.GenerationResult{Text: "Repository review accepted"}, nil
	}
	commentID.Store(2)
	poll()
	mustDrain(t, a)
	approveCodingCheck(t, s, r)
	after := drain()
	final := checkReceipt(t, s)
	if final.Outcome != "passed" || final.Output != "review_check" || final.CandidateSHA256 != initial.CandidateSHA256 || !s.hasContinuedPR(t, cfg.Scope(), ref, 1) || scalar(t, s, `SELECT count(*) FROM task`) != 1 || scalar(t, s, `SELECT count(*) FROM task_instruction`) != 1 || scalar(t, s, `SELECT count(*) FROM run WHERE worker_position='root'`) != 2 {
		t.Fatal("review lost original task or candidate", ref, initial, final)
	}
	reports := 0
	for _, m := range after {
		if strings.Contains(m, wantResult) {
			reports++
			if !strings.HasPrefix(m, "/open-apis/im/v1/messages/"+topic+"/reply ") {
				t.Fatal("review reported to a different topic", m)
			}
		}
	}
	if reports != 1 {
		t.Fatalf("expected one review result on the original topic; got %d in %q", reports, after)
	}

	before := len(model.requests)
	poll()
	if got := drain(); len(got) != len(after) || len(model.requests) != before {
		t.Fatal("duplicate review ran or sent again")
	}
	commentID.Store(3)
	poll()
	current, err := s.GitHubTaskBinding(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if scalar(t, s, `SELECT count(*) FROM github_observation o JOIN github_rule_decision d ON d.observation_sequence=o.sequence WHERE o.source_method_json IS NOT NULL AND d.matched=1`) != 2 {
		t.Fatal("second managed review never became eligible before unbinding", current)
	}
	if err := s.BindGitHubTask(ctx, cfg, github.BindingRequest{Version: current.Version}, false); err != nil {
		t.Fatal(err)
	}
	if got := drain(); len(got) != len(after) || len(model.requests) != before || scalar(t, s, `SELECT count(*) FROM task_instruction`) != 1 {
		t.Fatal("revoked pending review ran or delivered")
	}
}
