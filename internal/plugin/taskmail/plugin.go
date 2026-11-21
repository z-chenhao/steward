// Package taskmail reads bounded plain-text forwards as untrusted Task evidence.
package taskmail

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/taskmail/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func Plugin() rsiplugin.Handlers {
	return rsiplugin.Handlers{PluginID: contract.PluginID, Version: "1.0.0", Services: map[string]func(context.Context, rsiplugin.Call) (rsiplugin.Result, error){contract.Service: receive}}
}
func receive(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	if len(call.Inputs) != 1 || call.Inputs[0].ID != "message" || call.Inputs[0].Size > contract.MaxBytes {
		return rsiplugin.JSONResult(ctx, contract.Message{Error: "invalid_message"})
	}
	raw, err := rsiplugin.ReadInput(ctx, call.Inputs[0])
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, parse(raw))
}
func parse(raw []byte) contract.Message {
	bad := contract.Message{Error: "invalid_message"}
	m, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return bad
	}
	for _, k := range []string{"From", "Subject", "Message-Id", "Content-Type", "Content-Transfer-Encoding"} {
		if len(m.Header[k]) > 1 {
			return bad
		}
	}
	from, err := mail.ParseAddress(m.Header.Get("From"))
	if err != nil || len(from.Address) > 320 {
		return bad
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if err != nil || len(subject) > 998 || !utf8.ValidString(subject) || strings.ContainsAny(subject, "\x00\r\n") {
		return bad
	}
	id := strings.TrimSpace(m.Header.Get("Message-Id"))
	if len(id) > 998 || strings.ContainsAny(id, "\x00\r\n") {
		return bad
	}
	kind := m.Header.Get("Content-Type")
	if kind != "" {
		media, params, err := mime.ParseMediaType(kind)
		ch := strings.ToLower(params["charset"])
		if err != nil || media != "text/plain" || (ch != "" && ch != "us-ascii" && ch != "utf-8") {
			return contract.Message{Error: "unsupported_message"}
		}
	}
	var reader io.Reader = m.Body
	switch strings.ToLower(m.Header.Get("Content-Transfer-Encoding")) {
	case "", "7bit", "8bit":
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, reader)
	case "quoted-printable":
		reader = quotedprintable.NewReader(reader)
	default:
		return contract.Message{Error: "unsupported_message"}
	}
	body, err := io.ReadAll(reader)
	if err != nil || !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return bad
	}
	return contract.Message{MessageID: id, From: from.Address, Subject: subject, Text: strings.ReplaceAll(string(body), "\r\n", "\n")}
}
