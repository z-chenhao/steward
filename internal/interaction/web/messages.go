package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/conversation"
	"github.com/z-chenhao/assistant/internal/delivery/ui"
	"github.com/z-chenhao/assistant/internal/interaction"
)

func (s *Server) acceptJSONMessage(w http.ResponseWriter, r *http.Request) error {
	var input struct {
		SourceID string                     `json:"source_id"`
		Content  []conversation.ContentPart `json:"content"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		fail(w, 400)
		return nil
	}
	receipt, e := s.Core.AcceptInbound(r.Context(), interaction.InboundEvent{Channel: "web", Subject: Subject, Destination: Destination, EventID: input.SourceID, SourceID: input.SourceID, Content: input.Content})
	if e != nil {
		return e
	}
	if s.Wake != nil {
		s.Wake()
	}
	jsonResult(w, map[string]any{"accepted": true, "message_id": receipt.MessageID})
	return nil
}
func (s *Server) acceptFormMessage(w http.ResponseWriter, r *http.Request) error {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return err
	}
	defer r.MultipartForm.RemoveAll()
	key := r.FormValue("source_id")
	text := r.FormValue("text")
	target := "/"
	if ref := r.FormValue("task_ref"); ref != "" {
		if s.Tasks == nil {
			return errPageNotFound
		}
		d, err := s.Tasks.DetailPage(r.Context(), ref, "", 0)
		if err != nil {
			return err
		}
		text = fmt.Sprintf("Regarding task %q (%s):\n%s", d.Title, d.TaskRef, text)
		target = "/?task=" + url.QueryEscape(d.TaskRef)
	}
	e := interaction.InboundEvent{Channel: "web", Subject: Subject, Destination: Destination, EventID: key, SourceID: key, Content: conversation.Text(text)}
	if err := e.Validate(); err != nil {
		return err
	}
	file, header, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (30<<20)+1))
		if err != nil || len(data) > 30<<20 {
			return artifact.ErrLimit
		}
		input := artifact.Input{Name: header.Filename, MediaType: header.Header.Get("Content-Type")}
		ref, err := s.Core.ImportChannelArtifact(r.Context(), e, ui.Hash([]any{input.Name, input.MediaType, ui.Hash(data)}), input, bytes.NewReader(data))
		if err != nil {
			return err
		}
		descriptor, err := s.Core.DescribeArtifact(r.Context(), ref)
		if err != nil {
			return err
		}
		e.Content = append(e.Content, conversation.ContentPart{Type: descriptor.ContentType(), ArtifactRef: ref})
	} else if err != http.ErrMissingFile {
		return err
	}
	if _, err = s.Core.AcceptInbound(r.Context(), e); err != nil {
		return err
	}
	if s.Wake != nil {
		s.Wake()
	}
	if r.Header.Get("Accept") == "application/json" {
		jsonResult(w, map[string]any{"accepted": true, "location": target, "next_source_id": sourceID()})
		return nil
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
	return nil
}
func (s *Server) uploadArtifact(w http.ResponseWriter, r *http.Request) error {
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		return e
	}
	defer r.MultipartForm.RemoveAll()
	file, h, e := r.FormFile("file")
	if e != nil {
		return e
	}
	defer file.Close()
	d, e := s.Core.SaveArtifact(r.Context(), artifact.Input{Name: h.Filename, MediaType: h.Header.Get("Content-Type")}, file)
	if e != nil {
		return e
	}
	jsonResult(w, d)
	return nil
}
func (s *Server) decideApproval(r *http.Request) error {
	key := r.FormValue("source_id")
	if key == "" {
		key = "approval:" + r.FormValue("approval_id") + ":" + r.FormValue("decision")
	}
	text := r.FormValue("approval_id") + ":" + r.FormValue("hash") + ":" + r.FormValue("decision")
	event := interaction.InboundEvent{Channel: "web", Subject: Subject, Destination: Destination, EventID: key, SourceID: key, Content: conversation.Text(text)}
	if e := s.Core.DecideInbound(r.Context(), event, r.FormValue("approval_id"), r.FormValue("hash"), r.FormValue("decision")); e != nil {
		return e
	}
	if s.Wake != nil {
		s.Wake()
	}

	return nil
}
