package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func TestTaskMailboxRealForwardingCLIAndRevocation(t *testing.T) {
	ctx := context.Background()
	s, r, a := controlFixture(t, nil)
	controlMain(t, r, a, "mail", "create")
	host := installArtifactWorkbench(t, s, r)
	var taskRef, taskID, root string
	if err := s.db.QueryRow(`SELECT t.task_ref,t.task_id,r.run_id FROM task t JOIN run r USING(task_id) WHERE worker_position='root'`).Scan(&taskRef, &taskID, &root); err != nil {
		t.Fatal(err)
	}
	controlMain(t, r, a, "other-mail-task", "create second task")
	var otherRoot string
	if err := s.db.QueryRow(`SELECT run_id FROM run WHERE worker_position='root' AND task_id!=?`, taskID).Scan(&otherRoot); err != nil {
		t.Fatal(err)
	}
	cli := runtimeFeatureHost(t)
	source := t.TempDir()
	run := func(input string, args ...string) (string, error) {
		cmd := exec.Command(cli, args...)
		cmd.Env = append(os.Environ(), "STEWARD_DATA_ROOT="+s.DataRoot())
		cmd.Stdin = strings.NewReader(input)
		b, e := cmd.CombinedOutput()
		return string(b), e
	}
	check := func(input string, args ...string) string {
		t.Helper()
		out, err := run(input, args...)
		if err != nil {
			t.Fatal(args, out, err)
		}
		return out
	}
	buildTestBinary(t, "../../../cmd/plugins/task-mail", filepath.Join(source, "plugin"))
	raw, err := os.ReadFile("../../../plugins/task-mail/steward.plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "steward.plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	check("", "plugin", "seal", source)
	check("", "mailbox", "enable-plugin", source)
	check("", "mailbox", "enable-plugin", source)
	g, err := host.Store.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := host.Store.Graph(ctx, g.Revision)
	if err != nil || len(graph.Graph.Bindings) != 2 {
		t.Fatal("original artifact plugin not retained", graph, err)
	}
	var box TaskMailbox
	if err := json.Unmarshal([]byte(check("", "mailbox", "bind", taskRef, "forward.example.org")), &box); err != nil {
		t.Fatal(err)
	}
	mail := "From: Supplier <supplier@example.org>\r\nTo: another-task@example.org\r\nSubject: =?utf-8?q?Shipment_update?=\r\nMessage-ID: <status-44@example.org>\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nDelivery=20Friday.\r\n"
	first := check(mail, "mailbox", "receive", box.Address, "MTA-44")
	if duplicate := check(mail, "mailbox", "receive", box.Address, "MTA-44"); first != duplicate {
		t.Fatal("delivery receipt changed", first, duplicate)
	}
	if _, err := run(mail+"changed", "mailbox", "receive", box.Address, "MTA-44"); err == nil {
		t.Fatal("transport ID collision accepted")
	}
	var receipt TaskMailReceipt
	if err := json.Unmarshal([]byte(first), &receipt); err != nil || receipt.TaskRef != taskRef {
		t.Fatal(first, err)
	}
	var listed []TaskMailItem
	if err := json.Unmarshal([]byte(check("", "mailbox", "messages", taskRef)), &listed); err != nil || len(listed) != 1 || listed[0].ArtifactRef != receipt.ArtifactRef {
		t.Fatal(listed, err)
	}
	f, err := s.Artifacts().Open(ctx, toolRunScope(t, s, root), receipt.ArtifactRef)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(f)
	f.Close()
	if err != nil || !bytes.Contains(content, []byte("Delivery Friday.")) {
		t.Fatal(string(content), err)
	}
	if _, err = s.Artifacts().Open(ctx, toolRunScope(t, s, otherRoot), receipt.ArtifactRef); !errors.Is(err, artifact.ErrNotFound) {
		t.Fatal("other task could read inbound mail", err)
	}
	var wg sync.WaitGroup
	results := make([]TaskMailReceipt, 3)
	errs := make([]error, 3)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = s.ReceiveTaskMail(ctx, box.Address, "MTA-race", strings.NewReader(mail))
		}()
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil || results[i] != results[0] || results[i].ArtifactRef == "" {
			t.Fatal("concurrent transport receipt", results, errs)
		}
	}
	if err := host.Store.Stop(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := run(mail, "mailbox", "receive", box.Address, "MTA-stop"); err == nil {
		t.Fatal("Guardian stop accepted mail")
	}
	if err := host.Store.Stop(ctx, false); err != nil {
		t.Fatal(err)
	}
	check("", "mailbox", "close", box.Address)
	if _, err := run(mail, "mailbox", "receive", box.Address, "MTA-45"); err == nil {
		t.Fatal("closed mailbox accepted inbound")
	}
	fresh, err := s.BindTaskMailbox(ctx, taskRef, "forward.example.org")
	if err != nil || fresh.Address == box.Address {
		t.Fatal(fresh, err)
	}
	idAgentOutsideTxn := idAgent(t, s)
	s.MainBoundary = func(stage string) {
		if stage == "before_task_mail_commit" {
			if err := s.Transaction(ctx, func(tx *sql.Tx) error { return r.revokeMemory(ctx, tx, idAgentOutsideTxn) }); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, err = s.ReceiveTaskMail(ctx, fresh.Address, "MTA-46", strings.NewReader(mail))
	if !errors.Is(err, rsi.ErrRevoked) || scalar(t, s, `SELECT count(*) FROM task_mail_receipt`) != 2 {
		t.Fatal("late Forget released mail", err)
	}
	items, err := s.RecentTaskMail(ctx, taskRef, 0)
	if err != nil || len(items) != 0 {
		t.Fatal("forgotten evidence listed", items, err)
	}
}
