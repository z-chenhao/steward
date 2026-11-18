package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// websocketSession owns an HTTP-upgraded connection and an optional Responses
// continuation. The token serializes requests; CloseSession can cancel a waiter
// or an active read without sharing model state between unrelated sessions.
type websocketSession struct {
	token         chan struct{}
	conn          io.ReadWriteCloser
	cancel        context.CancelFunc // guarded by Client.mu
	previousBody  map[string]any
	previousItems []any
	responseID    string
	closed        bool // guarded by Client.mu
}

func (c *Client) CloseSession(id string) {
	c.closeSessions(id + "\x00")
}
func (c *Client) Close() {
	c.closeSessions("")
}
func (c *Client) closeSessions(prefix string) {
	c.mu.Lock()
	entries := []*websocketSession{}
	for key, entry := range c.sockets {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		delete(c.sockets, key)
		entry.closed = true
		if entry.cancel != nil {
			entry.cancel()
		}
		entries = append(entries, entry)
	}
	c.mu.Unlock()
	for _, entry := range entries {
		<-entry.token
		if entry.conn != nil {
			entry.conn.Close()
			entry.conn = nil
		}
		entry.token <- struct{}{}
	}
}
func (c *Client) websocket(ctx context.Context, m Model, wire Request, o Options, emit func(Event)) (AssistantMessage, bool, error) {
	state := newStreamState(m, emit)
	if m.API != CodexResponses && m.API != OpenAIResponses && m.API != AzureResponses {
		return state.message, false, ErrIncompatible
	}
	cache := (o.Transport == "websocket-cached" || o.Transport == "auto") && o.SessionID != ""
	headers := wire.Headers.Clone()
	headers.Del("Accept")
	headers.Del("Content-Type")
	headers.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
	rawHeaders, _ := json.Marshal(headers)
	key := o.SessionID + "\x00" + wire.URL + "\x00" + sha(rawHeaders)
	c.mu.Lock()
	if c.sockets == nil {
		c.sockets = map[string]*websocketSession{}
	}
	entry := c.sockets[key]
	if !cache || entry == nil {
		entry = &websocketSession{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		if cache {
			c.sockets[key] = entry
		}
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return state.message, false, ctx.Err()
	case <-entry.token:
	}
	defer func() { entry.token <- struct{}{} }()
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	if entry.closed {
		c.mu.Unlock()
		return state.message, false, context.Canceled
	}
	entry.cancel = cancel
	c.mu.Unlock()
	defer func() { c.mu.Lock(); entry.cancel = nil; c.mu.Unlock() }()
	failed := true
	defer func() {
		if !cache || failed {
			if entry.conn != nil {
				entry.conn.Close()
				entry.conn = nil
			}
			entry.previousBody = nil
			entry.previousItems = nil
			entry.responseID = ""
		}
	}()
	if entry.conn == nil {
		client := c.directHTTPClient(o.HTTPClient)
		handshakeCtx, handshakeCancel := context.WithCancel(work)
		defer handshakeCancel()
		timeout := o.HeaderTimeout
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		timer := time.AfterFunc(timeout, handshakeCancel)
		req, err := http.NewRequestWithContext(handshakeCtx, http.MethodGet, wire.URL, nil)
		if err != nil {
			timer.Stop()
			return state.message, false, ErrIncompatible
		}
		req.Header = headers
		nonce := make([]byte, 16)
		if _, err = rand.Read(nonce); err != nil {
			timer.Stop()
			return state.message, false, err
		}
		wsKey := base64.StdEncoding.EncodeToString(nonce)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", wsKey)
		response, err := client.Do(req)
		timer.Stop()
		if err != nil {
			if ctx.Err() != nil {
				return state.message, false, ctx.Err()
			}
			return state.message, false, &Error{Code: "PROVIDER_UNAVAILABLE"}
		}
		if o.OnResponse != nil {
			o.OnResponse(response)
		}
		if response.StatusCode != 101 {
			failure := responseError(response, io.LimitReader(response.Body, 8192))
			response.Body.Close()
			return state.message, false, failure
		}
		accept := sha1.Sum([]byte(wsKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		conn, ok := response.Body.(io.ReadWriteCloser)
		if !ok || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") || response.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(accept[:]) {
			response.Body.Close()
			return state.message, false, ErrInvalidResponse
		}
		entry.conn = conn
	}
	conn := entry.conn
	stop := context.AfterFunc(work, func() { conn.Close() })
	defer stop()
	body := cloneMap(wire.Body)
	delete(body, "stream")
	if cache && entry.responseID != "" && entry.previousBody != nil {
		withoutInput := func(v map[string]any) []byte {
			clone := cloneMap(v)
			delete(clone, "input")
			delete(clone, "previous_response_id")
			b, _ := json.Marshal(clone)
			return b
		}
		current := list(body["input"])
		baseline := append(append([]any{}, list(entry.previousBody["input"])...), entry.previousItems...)
		if bytes.Equal(withoutInput(body), withoutInput(entry.previousBody)) && len(current) >= len(baseline) {
			prefix, _ := json.Marshal(current[:len(baseline)])
			previous, _ := json.Marshal(baseline)
			if bytes.Equal(prefix, previous) {
				body["previous_response_id"] = entry.responseID
				body["input"] = current[len(baseline):]
			}
		}
	}
	body["type"] = "response.create"
	data, err := json.Marshal(body)
	if err != nil {
		return state.message, false, ErrIncompatible
	}
	if err = writeWebsocket(entry.conn, 1, data); err != nil {
		return state.message, false, &Error{Code: "PROVIDER_UNAVAILABLE"}
	}
	maxBytes := o.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	maxEvents := o.MaxEvents
	if maxEvents <= 0 {
		maxEvents = 100000
	}
	readBytes := int64(0)
	var idle *time.Timer
	if o.IdleTimeout > 0 {
		idle = time.AfterFunc(o.IdleTimeout, func() { conn.Close() })
		defer idle.Stop()
	}
	started := false
	var responseItems []any
	for count := 0; count < maxEvents; count++ {
		payload, err := readWebsocket(entry.conn, maxBytes-readBytes)
		if err != nil {
			if work.Err() != nil {
				return state.message, started, work.Err()
			}
			return state.message, started, ErrInvalidResponse
		}
		if idle != nil {
			idle.Reset(o.IdleTimeout)
		}
		readBytes += int64(len(payload))
		started = true
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		var v map[string]any
		if decoder.Decode(&v) != nil {
			return state.message, true, ErrInvalidResponse
		}
		if o.OnProviderEvent != nil {
			if err := o.OnProviderEvent(v); err != nil {
				return state.message, true, err
			}
		}
		if str(v["type"]) == "response.completed" {
			responseItems = list(record(v["response"])["output"])
		}
		if err := state.consume(m.API, v); err != nil {
			return state.message, true, err
		}
		if state.terminal {
			CalculateCost(m, &state.message.Usage)
			if err := state.finish(); err != nil {
				return state.message, true, err
			}
			entry.previousBody = cloneMap(wire.Body)
			delete(entry.previousBody, "stream")
			entry.previousItems = responseItems
			entry.responseID = state.message.ResponseID
			if str(v["type"]) != "response.completed" {
				// An incomplete response has no complete replay baseline.
				entry.previousBody, entry.previousItems, entry.responseID = nil, nil, ""
			}
			failed = false
			return state.message, true, nil
		}
	}
	return state.message, started, ErrInvalidResponse
}
func writeWebsocket(w io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode, 0x80}
	n := len(payload)
	switch {
	case n < 126:
		header[1] |= byte(n)
	case n <= 65535:
		header[1] |= 126
		header = binary.BigEndian.AppendUint16(header, uint16(n))
	default:
		header[1] |= 127
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	header = append(header, mask...)
	encoded := make([]byte, n)
	for i, b := range payload {
		encoded[i] = b ^ mask[i%4]
	}
	frame := append(header, encoded...)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
func readWebsocket(conn io.ReadWriter, limit int64) ([]byte, error) {
	var output []byte
	continuation := false
	for {
		head := make([]byte, 2)
		if _, err := io.ReadFull(conn, head); err != nil {
			return nil, err
		}
		fin := head[0]&0x80 != 0
		opcode := head[0] & 15
		if head[0]&0x70 != 0 || head[1]&0x80 != 0 {
			return nil, ErrInvalidResponse
		}
		size := uint64(head[1] & 127)
		if size == 126 {
			b := make([]byte, 2)
			if _, err := io.ReadFull(conn, b); err != nil {
				return nil, err
			}
			size = uint64(binary.BigEndian.Uint16(b))
		} else if size == 127 {
			b := make([]byte, 8)
			if _, err := io.ReadFull(conn, b); err != nil {
				return nil, err
			}
			size = binary.BigEndian.Uint64(b)
		}
		if size > uint64(max(0, limit-int64(len(output)))) || size > 64<<20 {
			return nil, ErrInvalidResponse
		}
		if opcode >= 8 && (!fin || size > 125) {
			return nil, ErrInvalidResponse
		}
		payload := make([]byte, int(size))
		if _, err := io.ReadFull(conn, payload); err != nil {
			return nil, err
		}
		switch opcode {
		case 8:
			return nil, io.EOF
		case 9:
			if err := writeWebsocket(conn, 10, payload); err != nil {
				return nil, err
			}
			continue
		case 10:
			continue
		case 1:
			if continuation {
				return nil, ErrInvalidResponse
			}
			continuation = true
		case 0:
			if !continuation {
				return nil, ErrInvalidResponse
			}
		default:
			return nil, ErrInvalidResponse
		}
		output = append(output, payload...)
		if fin {
			return output, nil
		}
	}
}
