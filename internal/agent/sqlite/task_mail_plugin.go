package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/taskmail/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
)

func (s *Store) EnableTaskMail(ctx context.Context, h rsi.Host, source string) (rsi.Status, error) {
	return s.enableOptionalReadPlugin(ctx, h, source, optionalReadPlugin{instance: "task-mail", pluginID: contract.PluginID, contract: contract.Service, goal: "Enable plain-text Task mail forwarding", cases: taskMailCases()})
}
func taskMailCases() []rsi.Case {
	makeCase := func(data, expected string) rsi.Case {
		return rsi.Case{InstanceID: "task-mail", Contract: contract.Service, Input: json.RawMessage(`{}`), Inputs: []rsi.InputData{{ID: "message", Data: []byte(data)}}, Expected: json.RawMessage(expected)}
	}
	return []rsi.Case{makeCase("From: test@example.org\r\nSubject: status\r\n\r\nreceived", `{"from":"test@example.org","subject":"status","text":"received"}`), makeCase("invalid", `{"error":"invalid_message"}`)}
}

func mailServiceAvailable(ctx context.Context, tx *sql.Tx) error {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_control c JOIN rsi_graph g ON g.revision=c.active_graph,
	json_each(g.graph_json,'$.bindings') b JOIN rsi_package p ON p.digest=json_extract(b.value,'$.package_digest'),
	json_each(p.manifest_json,'$.provides') x WHERE c.singleton=1 AND c.stopped=0 AND NOT EXISTS(SELECT 1 FROM rsi_quarantine q WHERE q.revision=g.revision)
	AND json_extract(x.value,'$.contract')=? GROUP BY g.revision HAVING count(*)=1)`, contract.Service).Scan(&ok)
	if err == nil && !ok {
		return rsi.ErrRevoked
	}
	return err
}
