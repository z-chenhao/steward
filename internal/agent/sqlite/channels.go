package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"io"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/delivery"
	"github.com/z-chenhao/assistant/internal/interaction"
)

func (s *Store) BindChannel(ctx context.Context, channel, subject, destination string, enabled bool, version int64) (interaction.Binding, error) {
	if !interaction.ValidIdentity(channel, subject, destination) {
		return interaction.Binding{}, interaction.ErrUnauthorized
	}
	b := interaction.Binding{Channel: channel, Subject: subject, Destination: destination, Enabled: enabled}
	err := s.Transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT binding_id,version FROM channel_binding WHERE channel=? AND subject=? AND destination=?`, channel, subject, destination).Scan(&b.ID, &b.Version)
		if err == sql.ErrNoRows {
			if version != 0 || !enabled {
				return agent.ErrConflict
			}
			b.ID = newID()
			b.Version = 1
			_, err = tx.ExecContext(ctx, `INSERT INTO channel_binding VALUES(?,?,?,?,?,1)`, b.ID, channel, subject, destination, enabled)
			return err
		}
		if err != nil {
			return err
		}
		if b.Version != version {
			return agent.ErrConflict
		}
		result, err := tx.ExecContext(ctx, `UPDATE channel_binding SET enabled=?,version=version+1 WHERE binding_id=? AND version=?`, enabled, b.ID, version)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return agent.ErrConflict
		}
		b.Version++
		return nil
	})
	return b, err
}
func (s *Store) ChannelBindings(ctx context.Context) ([]interaction.Binding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT binding_id,channel,subject,destination,enabled,version FROM channel_binding ORDER BY binding_id`)
	if err != nil {
		return nil, agent.ErrStorage
	}
	defer rows.Close()
	out := []interaction.Binding{}
	for rows.Next() {
		var b interaction.Binding
		if rows.Scan(&b.ID, &b.Channel, &b.Subject, &b.Destination, &b.Enabled, &b.Version) != nil {
			return nil, agent.ErrStorage
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) CheckChannel(ctx context.Context, channel, subject, destination string) error {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM channel_binding WHERE channel=? AND subject=? AND destination=? AND enabled=1`, channel, subject, destination).Scan(&n); err != nil {
		return agent.ErrStorage
	}
	if n != 1 {
		return interaction.ErrUnauthorized
	}
	return nil
}

// ChannelSourceContent reads one accepted input under the current channel
// binding. Presentation may recover an answer, but cannot invent acceptance.
func (s *Store) ChannelSourceContent(ctx context.Context, channel, subject, destination, source string) ([]conversation.ContentPart, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT i.message_json FROM channel_binding b JOIN channel_source s USING(binding_id) JOIN main_input i ON i.request_key=s.request_key WHERE b.channel=? AND b.subject=? AND b.destination=? AND b.enabled=1 AND s.source_id=?`, channel, subject, destination, source).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, agent.ErrStorage
	}
	var m conversation.Message
	if json.Unmarshal([]byte(raw), &m) != nil || m.Role != "user" {
		return nil, false, agent.ErrStorage
	}
	return m.Content, true, nil
}

type ChannelPreference = interaction.Preference

func (s *Store) ChannelPreference(ctx context.Context) (p ChannelPreference, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT channel,binding_id,version FROM channel_preference WHERE singleton=1`).Scan(&p.Channel, &p.BindingID, &p.Version)
	return
}
func (s *Store) PreferChannel(ctx context.Context, channel, binding string, version int64) error {
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		if channel != "auto" && channel != "tui" {
			var valid int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM channel_binding WHERE binding_id=? AND channel=? AND enabled=1`, binding, channel).Scan(&valid); err != nil {
				return err
			}
			if valid != 1 {
				return interaction.ErrUnauthorized
			}
		} else if binding != "" {
			return agent.ErrConflict
		}
		res, err := tx.ExecContext(ctx, `UPDATE channel_preference SET channel=?,binding_id=nullif(?,''),version=version+1 WHERE singleton=1 AND version=?`, channel, binding, version)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return agent.ErrConflict
		}
		return nil
	})
}

func acceptChannelSource(ctx context.Context, tx *sql.Tx, e interaction.InboundEvent, content, sourceKey string) (string, string, error) {
	var binding string
	if err := tx.QueryRowContext(ctx, `SELECT binding_id FROM channel_binding WHERE channel=? AND subject=? AND destination=? AND enabled=1`, e.Channel, e.Subject, e.Destination).Scan(&binding); err != nil {
		return "", "", interaction.ErrUnauthorized
	}
	hash := digest(content)
	var oldBinding, oldSource, oldHash string
	err := tx.QueryRowContext(ctx, `SELECT binding_id,source_id,content_hash FROM channel_callback WHERE channel=? AND event_id=?`, e.Channel, e.EventID).Scan(&oldBinding, &oldSource, &oldHash)
	if err == nil && (oldBinding != binding || oldSource != e.SourceID || oldHash != hash) {
		return "", "", agent.ErrConflict
	}
	if err != nil && err != sql.ErrNoRows {
		return "", "", err
	}
	keyParts, _ := encoded([]string{binding, e.SourceID})
	key := "channel:" + digest(keyParts)
	if sourceKey != "" {
		key = sourceKey
	}
	var savedKey, savedHash string
	sourceErr := tx.QueryRowContext(ctx, `SELECT request_key,content_hash FROM channel_source WHERE binding_id=? AND source_id=?`, binding, e.SourceID).Scan(&savedKey, &savedHash)
	if sourceErr == nil && (savedKey != key || savedHash != hash) {
		return "", "", agent.ErrConflict
	}
	if sourceErr == sql.ErrNoRows {
		if _, sourceErr = tx.ExecContext(ctx, `INSERT INTO channel_source VALUES(?,?,?,?)`, binding, e.SourceID, key, hash); sourceErr != nil {
			return "", "", sourceErr
		}
	} else if sourceErr != nil {
		return "", "", sourceErr
	}
	if err == sql.ErrNoRows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO channel_callback VALUES(?,?,?,?,?)`, e.Channel, e.EventID, binding, e.SourceID, hash); err != nil {
			return "", "", err
		}
	}
	return binding, key, nil
}

// Freeze the reply route together with the semantic intent. Later preference
// changes and callbacks cannot redirect an already accepted message.
func freezeMessageRoute(ctx context.Context, tx *sql.Tx, messageID, runID string) error {
	var binding, source string
	err := tx.QueryRowContext(ctx, `SELECT coalesce(m.binding_id,''),coalesce(m.reply_source_id,'') FROM main_input i LEFT JOIN message_route m USING(message_id) JOIN run r ON r.run_id=i.run_id WHERE i.run_id=? AND i.sequence<=json_extract(r.checkpoint_json,'$.input_sequence') ORDER BY i.sequence DESC LIMIT 1`, runID).Scan(&binding, &source)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == sql.ErrNoRows {
		origin, e := runReplyOrigin(ctx, tx, runID)
		if e != nil {
			return e
		}
		var channel string
		var preferred sql.NullString
		if err = tx.QueryRowContext(ctx, `SELECT channel,binding_id FROM channel_preference WHERE singleton=1`).Scan(&channel, &preferred); err != nil {
			return err
		}
		if preferred.Valid {
			binding = preferred.String
			if binding == origin.binding {
				source = origin.source
			}
		} else if channel == "auto" {
			if origin.binding != "" {
				binding, source = origin.binding, origin.source
			} else {
				err = tx.QueryRowContext(ctx, `SELECT binding_id FROM channel_binding WHERE channel='feishu' AND enabled=1 ORDER BY binding_id LIMIT 1`).Scan(&binding)
				if err != nil && err != sql.ErrNoRows {
					return err
				}
			}
		}
	}
	if binding == "" {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO message_route(message_id,binding_id,reply_source_id) SELECT ?,?,? WHERE NOT EXISTS(SELECT 1 FROM message_route WHERE message_id=?)`, messageID, binding, source, messageID)
	return err
}

func (s *Store) DeliveryReplySource(ctx context.Context, request delivery.Request) (string, error) {
	b, err := s.DeliveryBinding(ctx, request)
	if err != nil {
		return "", err
	}
	var source string
	err = s.db.QueryRowContext(ctx, `SELECT reply_source_id FROM message_route WHERE message_id=? AND binding_id=?`, request.MessageID, b.ID).Scan(&source)
	return source, err
}
func applyMessageRoute(ctx context.Context, tx *sql.Tx, r *delivery.Request) error {
	err := tx.QueryRowContext(ctx, `SELECT b.channel,b.destination FROM message_route m JOIN channel_binding b USING(binding_id) WHERE m.message_id=?`, r.MessageID).Scan(&r.Channel, &r.DestinationRef)
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}

// Projections and transport receipts are Host-owned and immutable. They never
// become model context. No adapter can change a frozen envelope under its key.
func (s *Store) SaveProjection(ctx context.Context, id, hash string, version int, spec any) (json.RawMessage, error) {
	raw, err := encoded(spec)
	if err != nil {
		return nil, err
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var oldHash, old string
		err := tx.QueryRowContext(ctx, `SELECT envelope_hash,spec_json FROM delivery_projection WHERE delivery_id=?`, id).Scan(&oldHash, &old)
		if err == nil {
			if oldHash != hash {
				return delivery.ErrConflict
			}
			raw = old
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		if candidate, ok := spec.(ui.Spec); ok {
			var message string
			if err = tx.QueryRowContext(ctx, `SELECT message_id FROM delivery_request WHERE delivery_id=?`, id).Scan(&message); err != nil {
				return err
			}
			candidate, err = appendWidgets(ctx, tx, message, candidate)
			if err != nil {
				return err
			}
			raw, err = encoded(candidate)
			if err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO delivery_projection VALUES(?,?,?,?)`, id, hash, version, raw)
		return err
	})
	return json.RawMessage(raw), err
}
func (s *Store) ChannelReceipt(ctx context.Context, id string, part int, hash string) (string, error) {
	var savedHash, remote string
	err := s.db.QueryRowContext(ctx, `SELECT payload_hash,remote_id FROM channel_receipt WHERE delivery_id=? AND part=?`, id, part).Scan(&savedHash, &remote)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", delivery.ErrStorage
	}
	if savedHash != hash {
		return "", delivery.ErrConflict
	}
	return remote, nil
}
func (s *Store) AcceptChannelReceipt(ctx context.Context, id string, part int, hash, remote string) error {
	if remote == "" {
		return delivery.ErrConflict
	}
	return s.Transaction(ctx, func(tx *sql.Tx) error {
		var h, r string
		err := tx.QueryRowContext(ctx, `SELECT payload_hash,remote_id FROM channel_receipt WHERE delivery_id=? AND part=?`, id, part).Scan(&h, &r)
		if err == nil {
			if h != hash || r != remote {
				return delivery.ErrConflict
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO channel_receipt VALUES(?,?,?,?)`, id, part, hash, remote)
		return err
	})
}

// DeliveryBinding validates the entire frozen destination and returns the
// enrolled identity, so adapters also fence changes of platform app/account.
func (s *Store) DeliveryBinding(ctx context.Context, request delivery.Request) (interaction.Binding, error) {
	var b interaction.Binding
	err := s.db.QueryRowContext(ctx, `SELECT b.binding_id,b.channel,b.subject,b.destination,b.enabled,b.version FROM delivery_request d JOIN message_route m USING(message_id) JOIN channel_binding b USING(binding_id) WHERE d.delivery_id=? AND d.message_id=? AND d.conversation_id=? AND d.channel=? AND d.destination_ref=? AND d.intent=? AND d.idempotency_key=? AND d.channel=b.channel AND d.destination_ref=b.destination AND b.enabled=1`, request.DeliveryID, request.MessageID, request.ConversationID, request.Channel, request.DestinationRef, request.Intent, request.IdempotencyKey).Scan(&b.ID, &b.Channel, &b.Subject, &b.Destination, &b.Enabled, &b.Version)
	if err == sql.ErrNoRows {
		return b, interaction.ErrUnauthorized
	}
	if err != nil {
		return b, agent.ErrStorage
	}
	return b, nil
}

func (s *Store) AgentStatus(ctx context.Context) (string, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM run WHERE status IN ('queued','running','waiting')`).Scan(&n); err != nil {
		return "", agent.ErrStorage
	}
	if n > 0 {
		return "busy", nil
	}
	return "idle", nil
}
func (s *Store) WebProjection(ctx context.Context, m conversation.Message) (json.RawMessage, error) {
	// A GET never materializes projections or intents. Delivery freezes a
	// projection under its accepted envelope; undelivered history is composed
	// in memory until that independent runtime operation commits.
	var raw, hash string
	err := s.db.QueryRowContext(ctx, `SELECT p.spec_json,p.envelope_hash FROM delivery_projection p JOIN delivery_request d USING(delivery_id) WHERE d.message_id=?`, m.MessageID).Scan(&raw, &hash)
	if err == sql.ErrNoRows {
		spec, e := appendWidgets(ctx, s.db, m.MessageID, ui.Compose(delivery.Envelope{Message: m}))
		if e != nil {
			return nil, e
		}
		return json.Marshal(spec)
	}
	if err != nil {
		return nil, delivery.ErrStorage
	}
	if hash != ui.Hash(m) {
		return nil, delivery.ErrConflict
	}
	return json.RawMessage(raw), nil
}
func (s *Store) ImportChannelArtifact(ctx context.Context, e interaction.InboundEvent, key string, input artifact.Input, src io.Reader) (string, error) {
	if key == "" || len(key) > 256 {
		return "", artifact.ErrInvalid
	}
	var binding string
	if err := s.db.QueryRowContext(ctx, `SELECT binding_id FROM channel_binding WHERE channel=? AND subject=? AND destination=? AND enabled=1`, e.Channel, e.Subject, e.Destination).Scan(&binding); err != nil {
		return "", interaction.ErrUnauthorized
	}
	var ref string
	err := s.db.QueryRowContext(ctx, `SELECT artifact_ref FROM channel_resource WHERE binding_id=? AND source_id=? AND resource_key=?`, binding, e.SourceID, key).Scan(&ref)
	if err == nil {
		return ref, nil
	}
	if err != sql.ErrNoRows {
		return "", artifact.ErrStorage
	}
	d, err := s.Diagnostics(ctx)
	if err != nil {
		return "", err
	}
	ref, err = s.Artifacts().Put(ctx, artifact.Scope{UserID: s.OwnerID(), AgentID: d.AgentID, Owner: true}, input, src)
	if err != nil {
		return "", err
	}
	err = s.Transaction(ctx, func(tx *sql.Tx) error {
		var old string
		err := tx.QueryRowContext(ctx, `SELECT artifact_ref FROM channel_resource WHERE binding_id=? AND source_id=? AND resource_key=?`, binding, e.SourceID, key).Scan(&old)
		if err == nil {
			ref = old
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO channel_resource VALUES(?,?,?,?)`, binding, e.SourceID, key, ref)
		return err
	})
	return ref, err
}
