package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type uiFormInput struct {
	binding   UIFormBinding
	privacy   capabilityPrivacy
	inputHash string
	result    contract.FormResult
}

func (f *uiFormInput) check(ctx context.Context, tx *sql.Tx, r *Runs) error {
	if err := f.privacy.check(ctx, tx); err != nil {
		return err
	}
	if err := r.pluginDispatchFence(ctx, tx, f.binding.RunID); err != nil {
		return err
	}
	c, err := resolveUIComponent(ctx, tx, f.binding.Component.WidgetRef)
	if err != nil {
		return err
	}
	if c.Name != f.binding.Component.Name || ui.Hash(c.Props) != ui.Hash(f.binding.Component.Props) {
		return conversation.ErrConflict
	}
	var graph, message string
	if err = tx.QueryRowContext(ctx, `SELECT b.graph_revision,w.message_id FROM ui_widget w JOIN rsi_run b USING(run_id) WHERE w.ui_ref=? AND w.run_id=?`, strings.Split(f.binding.Component.WidgetRef, "#")[0], f.binding.RunID).Scan(&graph, &message); err != nil {
		return err
	}
	if graph != f.binding.GraphRevision || message != f.binding.Component.MessageID {
		return conversation.ErrConflict
	}
	return nil
}
func (f *uiFormInput) save(ctx context.Context, tx *sql.Tx, messageID string) error {
	raw, err := json.Marshal(f.result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ui_form_answer VALUES(?,?,?,?,?,?,?)`, f.binding.Component.FormID, f.binding.Component.MessageID, f.binding.RunID, f.binding.GraphRevision, f.inputHash, messageID, string(raw))
	return err
}

// AcceptWebForm keeps candidate IO outside the writer. Only successful ordinary
// channel admission freezes its result, in the same transaction as main_input.
func (r *Runs) AcceptWebForm(ctx context.Context, m conversation.Message, formID string, values map[string]string) (agent.Receipt, error) {
	if err := r.host.CheckChannel(ctx, "web", interaction.WebSubject, interaction.WebDestination); err != nil {
		return agent.Receipt{}, err
	}
	f := &uiFormInput{}
	err := r.evidenceReadTx(ctx, func(tx *sql.Tx) error { var err error; f.privacy, err = readCapabilityPrivacy(ctx, tx); return err })
	if err != nil {
		return agent.Receipt{}, err
	}
	f.binding, err = r.host.WebFormBinding(ctx, m, formID)
	if err != nil {
		return agent.Receipt{}, err
	}
	var parts []conversation.ContentPart
	if !f.binding.Managed {
		// Historical Runs did not bind the form service; no current selection is
		// allowed to reinterpret their accepted presentation protocol.
		parts, err = ui.Answer(f.binding.Component, values)
		if err != nil {
			return agent.Receipt{}, err
		}
		f = nil
	} else {
		normalized := make(map[string]string, len(values))
		for k, v := range values {
			if !utf8.ValidString(k) || !utf8.ValidString(v) {
				return agent.Receipt{}, conversation.ErrInvalid
			}
			normalized[k] = strings.ReplaceAll(strings.ReplaceAll(v, "\r\n", "\n"), "\r", "\n")
		}
		f.inputHash, err = rsiplugin.Digest("steward.ui-form-input/1", normalized)
		if err != nil {
			return agent.Receipt{}, err
		}
		saved, found, loadErr := r.host.webFormAnswer(ctx, f.binding)
		if loadErr != nil {
			return agent.Receipt{}, loadErr
		}
		if found && saved.inputHash == f.inputHash {
			f.result = saved.result
		} else {
			if r.plugins == nil {
				return agent.Receipt{}, rsi.ErrNotFound
			}
			h := *r.plugins
			store := r.host.Plugins()
			store.Fence = f.privacy.check
			h.Store = store
			owner, err := h.ServiceInstance(ctx, f.binding.GraphRevision, contract.Normalize)
			if err != nil {
				return agent.Receipt{}, err
			}
			formatter, err := h.ServiceInstance(ctx, f.binding.GraphRevision, contract.FormContract)
			if err != nil {
				return agent.Receipt{}, err
			}
			if owner.InstanceID != formatter.InstanceID || owner.PackageDigest != formatter.PackageDigest {
				return agent.Receipt{}, rsi.ErrInvalid
			}
			facts, err := json.Marshal(contract.FormFacts{Name: f.binding.Component.Name, Props: f.binding.Component.Props, Values: normalized})
			if err != nil {
				return agent.Receipt{}, err
			}
			r.host.boundary("before_ui_form_policy")
			raw, err := h.ReadService(ctx, f.binding.GraphRevision, contract.FormContract, json.RawMessage(`{"input_id":"ui-form"}`), []rsi.InputData{{ID: contract.FormInput, Data: facts}})
			if err != nil {
				return agent.Receipt{}, err
			}
			if err = rsiplugin.DecodeResult(raw, &f.result); err != nil {
				return agent.Receipt{}, err
			}
		}
		parts, err = formResultContent(f.result)
		if err != nil {
			return agent.Receipt{}, err
		}
	}
	source := "ui-form:" + formID
	e := interaction.InboundEvent{Channel: "web", Subject: interaction.WebSubject, Destination: interaction.WebDestination, EventID: source, SourceID: source, Content: parts}
	if err = e.Validate(); err != nil {
		return agent.Receipt{}, err
	}
	r.host.boundary("before_ui_form_admission")
	return r.submit(ctx, "", parts, &e, "", nil, f)
}

func formResultContent(result contract.FormResult) ([]conversation.ContentPart, error) {
	if result.ErrorCode != "" {
		if len(result.Parts) != 0 || result.Response != "" {
			return nil, rsi.ErrInvalid
		}
		if result.ErrorCode == "INVALID_ARGUMENT" {
			return nil, conversation.ErrInvalid
		}
		if result.ErrorCode == "CONFLICT" {
			return nil, conversation.ErrConflict
		}
		return nil, rsi.ErrInvalid
	}
	if len(result.Parts) == 0 || len(result.Parts) > 17 || len(result.Response) > 128<<10 || !utf8.ValidString(result.Response) || strings.ContainsRune(result.Response, 0) {
		return nil, rsi.ErrInvalid
	}
	parts := make([]conversation.ContentPart, len(result.Parts))
	size := 0
	for i, text := range result.Parts {
		size += len(text)
		if size > 16<<10 || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			return nil, rsi.ErrInvalid
		}
		parts[i] = conversation.ContentPart{Type: "text", Text: text}
	}
	// Admission must not normalize a candidate's saved answer differently from
	// the representation used for retry and restored-response matching.
	normalized, err := conversation.Normalize(parts)
	if err != nil || !reflect.DeepEqual(normalized, parts) {
		return nil, rsi.ErrInvalid
	}
	return parts, nil
}

func (s *Store) webFormAnswer(ctx context.Context, b UIFormBinding) (uiFormInput, bool, error) {
	out := uiFormInput{binding: b}
	var raw, source, run, graph string
	err := s.db.QueryRowContext(ctx, `SELECT source_message_id,source_run_id,graph_revision,input_hash,result_json FROM ui_form_answer WHERE form_id=?`, b.Component.FormID).Scan(&source, &run, &graph, &out.inputHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	if source != b.Component.MessageID || run != b.RunID || graph != b.GraphRevision {
		return out, false, conversation.ErrConflict
	}
	if err = rsiplugin.DecodeResult([]byte(raw), &out.result); err != nil {
		return out, false, err
	}
	if _, err = formResultContent(out.result); err != nil {
		return out, false, err
	}
	return out, true, nil
}

// RestoreWebForm never runs a live candidate for a managed accepted answer.
// Content must come from the existing enabled-channel source ledger.
func (s *Store) RestoreWebForm(ctx context.Context, m conversation.Message, c *ui.Component, content []conversation.ContentPart) error {
	b, err := s.WebFormBinding(ctx, m, c.FormID)
	if err != nil {
		return err
	}
	if !b.Managed {
		return ui.Restore(c, content)
	}
	saved, found, err := s.webFormAnswer(ctx, b)
	if err != nil {
		return err
	}
	if !found {
		return agent.ErrNotFound
	}
	expected, err := formResultContent(saved.result)
	if err != nil || !reflect.DeepEqual(expected, content) {
		return conversation.ErrConflict
	}
	c.Response = saved.result.Response
	c.Submitted = true
	return nil
}
