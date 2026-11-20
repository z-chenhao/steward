// Package feishu implements the signed callback and HTTP delivery boundaries.
// Tenant/app identity is installation configuration; callback fields cannot
// select an account, API endpoint, Conversation, or notification recipient.
package feishu

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/z-chenhao/assistant/internal/artifact"
	"github.com/z-chenhao/assistant/internal/interaction"
	"github.com/z-chenhao/assistant/internal/llm"
)

type Config struct {
	AppID                string `yaml:"app_id"`
	TenantKey            string `yaml:"tenant_key"`
	VerificationTokenRef string `yaml:"verification_token_ref"`
	EncryptKeyRef        string `yaml:"encrypt_key_ref"`
	AccessTokenRef       string `yaml:"access_token_ref"`
	AppSecretRef         string `yaml:"app_secret_ref"`
	APIBase              string `yaml:"api_base"`
	WorkspaceURL         string `yaml:"workspace_url"`
}

func (c Config) Validate() error {
	if c.AppID == "" || c.TenantKey == "" || !interaction.ValidCredentialRef(c.VerificationTokenRef) || !interaction.ValidCredentialRef(c.EncryptKeyRef) {
		//lint:ignore ST1005 Preserve the existing user-facing diagnostic text.
		return errors.New("Feishu requires app, tenant and credential references")
	}
	return c.ValidateOutgoing()
}

// ValidateOutgoing checks the narrower configuration retained by a send-only
// watchdog route, including its account, credential references and API origin.
func (c Config) ValidateOutgoing() error {
	if c.AppID == "" || c.TenantKey == "" {
		return errors.New("missing Feishu account")
	}
	if (c.AccessTokenRef == "") == (c.AppSecretRef == "") {
		//lint:ignore ST1005 Preserve the existing user-facing diagnostic text.
		return errors.New("Feishu requires exactly one of access_token_ref or app_secret_ref")
	}
	ref := c.AccessTokenRef
	if c.AppSecretRef != "" {
		ref = c.AppSecretRef
	}
	if !interaction.ValidCredentialRef(ref) {
		//lint:ignore ST1005 Preserve the existing user-facing diagnostic text.
		return errors.New("Feishu requires a credential reference")
	}
	if c.APIBase != "" && c.APIBase != "https://open.feishu.cn" {
		u, e := url.Parse(c.APIBase)
		if e != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "[::1]" && u.Hostname() != "::1") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			//lint:ignore ST1005 Preserve the existing user-facing diagnostic text.
			return errors.New("Feishu API must be official HTTPS or explicit loopback sandbox")
		}
	}
	if c.WorkspaceURL != "" {
		u, err := url.Parse(c.WorkspaceURL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(c.WorkspaceURL, "#") || u.Path != "" && u.Path != "/" || u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
			return errors.New("feishu workspace_url requires an HTTPS or loopback HTTP origin without credentials, query or fragment")
		}
	}
	return nil
}

var ErrProtocol = errors.New("channel platform unavailable or invalid response")

type Client struct {
	Config  Config
	Secrets llm.SecretStore
	HTTP    *http.Client
	tokens  *tokenCache
}

func (c Client) secret(ctx context.Context, ref string) ([]byte, error) {
	if c.Secrets == nil {
		return nil, ErrProtocol
	}
	v, e := c.Secrets.Get(ctx, ref)
	if e != nil || len(v) == 0 {
		return nil, ErrProtocol
	}
	return v, nil
}
func wipe(v []byte) {
	for i := range v {
		v[i] = 0
	}
}
func (c Client) base() string {
	if c.Config.APIBase != "" {
		return c.Config.APIBase
	}
	return "https://open.feishu.cn"
}
func (c Client) http() *http.Client {
	client := http.Client{}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	if client.Timeout <= 0 || client.Timeout > 15*time.Second {
		client.Timeout = 15 * time.Second
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}
func (c Client) request(ctx context.Context, method, path, kind string, body io.Reader) (*http.Response, error) {
	return c.requestAuthorized(ctx, method, path, kind, body, nil)
}

func (c Client) requestAuthorized(ctx context.Context, method, path, kind string, body io.Reader, authorize func(context.Context) error) (*http.Response, error) {
	token, e := c.accessToken(ctx)
	if e != nil {
		return nil, e
	}
	defer wipe(token)
	req, e := http.NewRequestWithContext(ctx, method, c.base()+"/open-apis"+path, body)
	if e != nil {
		return nil, ErrProtocol
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	if kind != "" {
		req.Header.Set("Content-Type", kind)
	}
	if authorize != nil {
		if err := authorize(ctx); err != nil {
			return nil, err
		}
	}
	resp, e := c.http().Do(req)
	if e != nil {
		return nil, ErrProtocol
	}
	return resp, nil
}
func (c Client) decodeResponse(resp *http.Response, err error) (map[string]json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Code *int                       `json:"code"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r) != nil {
		if resp.StatusCode == http.StatusUnauthorized {
			c.invalidate(resp)
			return nil, ErrAuthentication
		}
		return nil, ErrProtocol
	}
	code := -1
	if r.Code != nil {
		code = *r.Code
	}
	if authenticationRejected(resp.StatusCode, code) {
		c.invalidate(resp)
		return nil, ErrAuthentication
	}
	if resp.StatusCode != 200 || r.Code == nil || code != 0 || r.Data == nil {
		return nil, ErrProtocol
	}
	return r.Data, nil
}
func (c Client) Send(ctx context.Context, destination, key, kind string, content any) (string, error) {
	raw, e := json.Marshal(content)
	if e != nil {
		return "", ErrProtocol
	}
	payload, _ := json.Marshal(map[string]any{"receive_id": destination, "uuid": key, "msg_type": kind, "content": string(raw)})
	data, e := c.decodeResponse(c.request(ctx, "POST", "/im/v1/messages?receive_id_type=chat_id", "application/json", bytes.NewReader(payload)))
	var id string
	if e != nil {
		return "", e
	}
	if json.Unmarshal(data["message_id"], &id) != nil || id == "" {
		return "", ErrProtocol
	}
	return id, nil
}

// Reply quotes only the Host-frozen source message. It never falls back to a
// destination supplied by a callback or to a new send if the source is gone.
func (c Client) Reply(ctx context.Context, source, key, kind string, content any) (string, error) {
	return c.reply(ctx, source, key, kind, content, nil)
}

func (c Client) reply(ctx context.Context, source, key, kind string, content any, authorize func(context.Context) error) (string, error) {
	if source == "" || len(source) > 256 || source == "." || source == ".." || strings.ContainsAny(source, "/\\\r\n\x00") {
		return "", ErrProtocol
	}
	raw, err := json.Marshal(content)
	if err != nil {
		return "", ErrProtocol
	}
	payload, _ := json.Marshal(map[string]any{"uuid": key, "msg_type": kind, "content": string(raw), "reply_in_thread": false})
	data, err := c.decodeResponse(c.requestAuthorized(ctx, "POST", "/im/v1/messages/"+url.PathEscape(source)+"/reply", "application/json", bytes.NewReader(payload), authorize))
	if err != nil {
		return "", err
	}
	var id string
	if json.Unmarshal(data["message_id"], &id) != nil || id == "" {
		return "", ErrProtocol
	}
	return id, nil
}
func (c Client) Upload(ctx context.Context, d artifact.Descriptor, src io.Reader) (string, error) {
	// Bounded ArtifactStore size is checked before materializing multipart bytes.
	if d.ByteSize < 0 || d.ByteSize > 30<<20 {
		return "", ErrProtocol
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("file_type", "stream")
	_ = w.WriteField("file_name", d.Name)
	part, e := w.CreateFormFile("file", d.Name)
	if e != nil {
		return "", ErrProtocol
	}
	n, e := io.Copy(part, io.LimitReader(src, d.ByteSize+1))
	if e != nil || n != d.ByteSize {
		return "", ErrProtocol
	}
	if w.Close() != nil {
		return "", ErrProtocol
	}
	data, e := c.decodeResponse(c.request(ctx, "POST", "/im/v1/files", w.FormDataContentType(), &buf))
	var key string
	if e != nil {
		return "", e
	}
	if json.Unmarshal(data["file_key"], &key) != nil || key == "" {
		return "", ErrProtocol
	}
	return key, nil
}
func (c Client) Download(ctx context.Context, message, key, kind string) ([]byte, string, error) {
	if message == "" || key == "" || len(message) > 256 || len(key) > 256 {
		return nil, "", ErrProtocol
	}
	path := "/im/v1/messages/" + url.PathEscape(message) + "/resources/" + url.PathEscape(key) + "?type=" + url.QueryEscape(kind)
	resp, e := c.request(ctx, "GET", path, "", nil)
	if e != nil {
		return nil, "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		c.invalidate(resp)
		return nil, "", ErrAuthentication
	}
	if resp.StatusCode != 200 {
		return nil, "", ErrProtocol
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (30<<20)+1))
	if e != nil || len(b) > 30<<20 {
		return nil, "", ErrProtocol
	}
	media := resp.Header.Get("Content-Type")
	if media == "" {
		media = "application/octet-stream"
	}
	return b, media, nil
}

// Verify authenticates exact bytes before parsing or decrypting them. Freshness
// prevents indefinite replay; durable event/source dedup handles valid retries.
func (c Client) Verify(ctx context.Context, h http.Header, raw []byte, now time.Time) ([]byte, error) {
	timestamp, nonce := h.Get("X-Lark-Request-Timestamp"), h.Get("X-Lark-Request-Nonce")
	seconds, timestampError := strconv.ParseInt(timestamp, 10, 64)
	key, e := c.secret(ctx, c.Config.EncryptKeyRef)
	if e != nil {
		return nil, e
	}
	defer wipe(key)
	hash := sha256.New()
	hash.Write([]byte(timestamp + nonce))
	hash.Write(key)
	hash.Write(raw)
	want := hex.EncodeToString(hash.Sum(nil))
	signed := timestampError == nil && nonce != "" && len(nonce) <= 256 && now.Unix()-seconds <= 300 && seconds-now.Unix() <= 300 && subtle.ConstantTimeCompare([]byte(want), []byte(h.Get("X-Lark-Signature"))) == 1
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, ErrProtocol
	}
	if envelope.Encrypt != "" {
		encrypted, e := base64.StdEncoding.DecodeString(envelope.Encrypt)
		if e != nil || len(encrypted) < 32 || (len(encrypted)-16)%16 != 0 {
			return nil, ErrProtocol
		}
		aesKey := sha256.Sum256(key)
		block, e := aes.NewCipher(aesKey[:])
		if e != nil {
			return nil, ErrProtocol
		}
		data := make([]byte, len(encrypted)-16)
		cipher.NewCBCDecrypter(block, encrypted[:16]).CryptBlocks(data, encrypted[16:])
		n := int(data[len(data)-1])
		if n == 0 || n > 16 || n > len(data) {
			return nil, ErrProtocol
		}
		for _, b := range data[len(data)-n:] {
			if int(b) != n {
				return nil, ErrProtocol
			}
		}
		raw = data[:len(data)-n]
	}
	var auth struct {
		Token  string `json:"token"`
		Header struct {
			Token  string `json:"token"`
			AppID  string `json:"app_id"`
			Tenant string `json:"tenant_key"`
		} `json:"header"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &auth) != nil {
		return nil, ErrProtocol
	}
	token, e := c.secret(ctx, c.Config.VerificationTokenRef)
	if e != nil {
		return nil, e
	}
	defer wipe(token)
	presented := auth.Header.Token
	if auth.Type == "url_verification" {
		// Feishu URL verification is token-authenticated and may omit signature headers.
		presented = auth.Token
	} else if auth.Header.AppID != c.Config.AppID || auth.Header.Tenant != c.Config.TenantKey {
		return nil, ErrProtocol
	}
	if subtle.ConstantTimeCompare(token, []byte(presented)) != 1 {
		return nil, ErrProtocol
	}
	if auth.Type != "url_verification" && !signed {
		return nil, ErrProtocol
	}
	return raw, nil
}
func (c Client) Subject(openID string) string {
	return c.Config.TenantKey + ":" + c.Config.AppID + ":" + openID
}
func keyFor(id string, part int) string {
	h := sha256.Sum256([]byte(id + ":" + strconv.Itoa(part)))
	return hex.EncodeToString(h[:16])
}
func textCommand(text string) (string, string, string, bool) {
	p := strings.Fields(text)
	if len(p) != 3 || (p[0] != "/approve" && p[0] != "/deny") {
		return "", "", "", false
	}
	decision := "approved"
	if p[0] == "/deny" {
		decision = "denied"
	}
	return p[1], p[2], decision, true
}
