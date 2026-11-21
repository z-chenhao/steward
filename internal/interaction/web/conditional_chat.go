package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
)

type staticProjectionReader interface {
	StaticWebProjectionVersions(context.Context, []string) ([]string, bool, error)
}

// A bounded ring of hashes only. No message, approval, HTML or other private
// payload survives the request. A new process always renders before reuse.
type chatValidators struct {
	sync.Mutex
	entries [32]struct {
		facts [32]byte
		etag  string
	}
	next int
}

func (c *chatValidators) matches(facts [32]byte, etag string) bool {
	if etag == "" {
		return false
	}
	c.Lock()
	defer c.Unlock()
	for _, e := range c.entries {
		if e.facts == facts && e.etag == etag {
			return true
		}
	}
	return false
}
func (c *chatValidators) store(facts [32]byte, etag string) {
	c.Lock()
	defer c.Unlock()
	c.entries[c.next].facts = facts
	c.entries[c.next].etag = etag
	c.next = (c.next + 1) % len(c.entries)
}

type deferredChatMessage struct {
	Index   int
	Message conversation.Message
}
type conditionalChat struct {
	facts         [32]byte
	ids, versions []string
	messages      []deferredChatMessage
	eligible      bool
}

func (s *Server) loadConditionalChat(r *http.Request, p *page, c *conditionalChat) (bool, error) {
	reader, ok := s.Core.(staticProjectionReader)
	if !ok {
		return false, s.loadConversation(r.Context(), p)
	}
	err := s.loadConversationUsing(r.Context(), p, func(ctx context.Context, m conversation.Message, a map[string]interaction.Approval) (shownMessage, error) {
		// Host approvals also feed attention and must be projected before the
		// validator can be computed.
		if m.Source == "security" {
			return s.showMessage(ctx, m, a)
		}
		c.ids = append(c.ids, m.MessageID)
		c.messages = append(c.messages, deferredChatMessage{len(p.Messages), m})
		return shownMessage{}, nil
	})
	if err != nil {
		return false, err
	}
	c.versions, c.eligible, err = reader.StaticWebProjectionVersions(r.Context(), c.ids)
	if err != nil {
		return false, err
	}
	if c.eligible {
		location := requestLocation(r)
		var revision any
		if w := p.Workbench; w != nil {
			v := w.Presentation
			revision = []any{w.ReaderRevision, v.View.Digest, v.Stylesheet.Digest, v.Copy.Digest, v.Policy}
		}
		h := sha256.New()
		if err := json.NewEncoder(h).Encode([]any{p.Tab, p.View, revision, p.Status, p.Cursor, p.Before, p.Latest, p.Messages, p.Approvals, p.Attention, p.PendingCount, p.FirstPending, c.messages, c.versions, requestLanguage(r), location.String(), time.Now().In(location).Format("2006-01-02")}); err != nil {
			return false, err
		}
		h.Sum(c.facts[:0])
		if s.chatValidators.matches(c.facts, r.Header.Get("If-None-Match")) {
			return true, nil
		}
	}
	for _, entry := range c.messages {
		msg, err := s.showMessage(r.Context(), entry.Message, nil)
		if err != nil {
			return false, err
		}
		p.Messages[entry.Index] = msg
	}
	return false, nil
}
func (s *Server) rememberChat(ctx context.Context, c conditionalChat, etag string) {
	if !c.eligible {
		return
	}
	reader := s.Core.(staticProjectionReader)
	versions, ok, err := reader.StaticWebProjectionVersions(ctx, c.ids)
	if err == nil && ok && slices.Equal(versions, c.versions) {
		s.chatValidators.store(c.facts, etag)
	}
}
