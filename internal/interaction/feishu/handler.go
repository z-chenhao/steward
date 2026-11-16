package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/interaction"
)

type Core interface {
	AcceptInbound(context.Context, interaction.InboundEvent) (agent.Receipt, error)
	RespondTaskFeedbackInbound(context.Context, interaction.InboundEvent, string, string) (agent.Receipt, error)
	CheckChannel(context.Context, string, string, string) error
	DecideInbound(context.Context, interaction.InboundEvent, string, string, string) error
	ImportChannelArtifact(context.Context, interaction.InboundEvent, string, artifact.Input, io.Reader) (string, error)
	DescribeArtifact(context.Context, string) (artifact.Descriptor, error)
}
type Handler struct {
	ProjectCard CardProjector
	Client      Client
	Core        Core
	Wake        func()
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if e != nil {
		w.WriteHeader(413)
		return
	}
	raw, e = h.Client.Verify(r.Context(), r.Header, raw, time.Now())
	if e != nil {
		http.Error(w, "Callback authentication failed", http.StatusUnauthorized)
		return
	}
	var event struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Schema    string `json:"schema"`
		Header    struct {
			EventID string `json:"event_id"`
			Type    string `json:"event_type"`
		} `json:"header"`
		Event struct {
			Sender struct {
				Type string `json:"sender_type"`
				ID   struct {
					OpenID string `json:"open_id"`
				} `json:"sender_id"`
			} `json:"sender"`
			Message struct {
				ID       string `json:"message_id"`
				ChatID   string `json:"chat_id"`
				ChatType string `json:"chat_type"`
				Type     string `json:"message_type"`
				Content  string `json:"content"`
			} `json:"message"`
		} `json:"event"`
	}
	if json.Unmarshal(raw, &event) != nil {
		w.WriteHeader(400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if event.Type == "url_verification" {
		_ = json.NewEncoder(w).Encode(map[string]string{"challenge": event.Challenge})
		return
	}
	if event.Schema == "2.0" && event.Header.Type == "card.action.trigger" {
		h.taskQuestionAction(w, r, event.Header.EventID, raw)
		return
	}
	if event.Schema != "2.0" || event.Header.Type != "im.message.receive_v1" || event.Event.Sender.Type != "user" || event.Event.Message.ChatType != "p2p" || event.Event.Sender.ID.OpenID == "" {
		w.WriteHeader(400)
		return
	}
	m := event.Event.Message
	inbound := interaction.InboundEvent{Channel: "feishu", Subject: h.Client.Subject(event.Event.Sender.ID.OpenID), Destination: m.ChatID, EventID: event.Header.EventID, SourceID: m.ID}
	if h.Core.CheckChannel(r.Context(), inbound.Channel, inbound.Subject, inbound.Destination) != nil {
		w.WriteHeader(403)
		return
	}
	var content struct {
		Text     string `json:"text"`
		FileKey  string `json:"file_key"`
		ImageKey string `json:"image_key"`
		FileName string `json:"file_name"`
	}
	if json.Unmarshal([]byte(m.Content), &content) != nil {
		w.WriteHeader(400)
		return
	}
	if m.Type == "text" {
		if strings.TrimSpace(content.Text) == "" || len(content.Text) > 16<<10 {
			w.WriteHeader(400)
			return
		}
		inbound.Content = conversation.Text(content.Text)
		if id, hash, decision, ok := textCommand(content.Text); ok {
			if h.Core.DecideInbound(r.Context(), inbound, id, hash, decision) != nil {
				w.WriteHeader(409)
				return
			}
			if h.Wake != nil {
				h.Wake()
			}
			_, _ = io.WriteString(w, `{"accepted":true}`)
			return
		}
	} else {
		kind, key, name := "file", content.FileKey, content.FileName
		if m.Type == "image" {
			kind, key, name = "image", content.ImageKey, "image"
		} else if m.Type != "file" {
			w.WriteHeader(400)
			return
		}
		// Identity is checked before fetching any platform resources. The download
		// endpoint is fixed to this verified message; caller URLs are never followed.
		data, media, err := h.Client.Download(r.Context(), m.ID, key, kind)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		if name == "" {
			name = "attachment"
		}
		ref, err := h.Core.ImportChannelArtifact(r.Context(), inbound, key, artifact.Input{Name: name, MediaType: media}, bytes.NewReader(data))
		if err != nil {
			w.WriteHeader(409)
			return
		}
		descriptor, err := h.Core.DescribeArtifact(r.Context(), ref)
		if err != nil {
			w.WriteHeader(409)
			return
		}
		inbound.Content = []conversation.ContentPart{{Type: descriptor.ContentType(), ArtifactRef: ref}}
	}
	if _, e = h.Core.AcceptInbound(r.Context(), inbound); e != nil {
		w.WriteHeader(409)
		return
	}
	if h.Wake != nil {
		h.Wake()
	}
	_, _ = io.WriteString(w, `{"accepted":true}`)
}
