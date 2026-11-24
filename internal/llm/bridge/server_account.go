package bridge

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/llm"
	"github.com/z-chenhao/assistant/internal/llm/provider"
)

const authURL = "https://auth.openai.com"
const apiURL = "https://api.openai.com/v1"
const accountScope = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"

type accountAuth struct {
	mu      sync.Mutex
	client  *http.Client
	pending *authPending
}
type authPending struct {
	state, nonce, verifier, redirect, status string
	used, progress                           bool
	expires                                  time.Time
	server                                   *http.Server
	timer                                    *time.Timer
	cancel                                   context.CancelFunc
	work                                     sync.WaitGroup
	result                                   *llm.OAuthCredential
}

func randomToken() string {
	data := make([]byte, 32)
	_, _ = rand.Read(data)
	return base64.RawURLEncoding.EncodeToString(data)
}
func digestString(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func authError() error             { return &provider.Error{Code: "AUTH_REQUIRED"} }
func (a *accountAuth) request(ctx context.Context, path string, form url.Values) (map[string]json.RawMessage, error) {
	u, err := url.Parse(path)
	if err != nil || u.Scheme != "https" || u.Host != "auth.openai.com" || u.User != nil || u.Fragment != "" {
		return nil, authError()
	}
	method := http.MethodGet
	var body io.Reader
	if form != nil {
		method = http.MethodPost
		body = strings.NewReader(form.Encode())
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, method, path, body)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := *a.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return nil, &provider.Error{Code: "PROVIDER_UNAVAILABLE"}
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return nil, &provider.Error{Code: "RATE_LIMITED"}
	}
	if res.StatusCode >= 500 {
		return nil, &provider.Error{Code: "PROVIDER_UNAVAILABLE"}
	}
	if res.StatusCode != 200 {
		return nil, authError()
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, authError()
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(data, &out) != nil {
		return nil, authError()
	}
	return out, nil
}
func (a *accountAuth) start(ctx context.Context, raw json.RawMessage) (llm.AccountLogin, error) {
	a.cancel()
	var p struct {
		DeviceID     string                   `json:"device_id"`
		Registration *llm.AccountRegistration `json:"registration"`
		Credential   *llm.OAuthCredential     `json:"credential"`
	}
	if json.Unmarshal(raw, &p) != nil || !regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(p.DeviceID) {
		return llm.AccountLogin{}, authError()
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return llm.AccountLogin{}, authError()
	}
	work, cancel := context.WithCancel(ctx)
	pending := &authPending{state: randomToken(), nonce: randomToken(), verifier: randomToken(), status: "pending", expires: time.Now().Add(5 * time.Minute), redirect: "http://" + listener.Addr().String() + "/auth/callback", cancel: cancel}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		send := func(status int) {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.WriteHeader(status)
			if status == 200 {
				_, _ = io.WriteString(w, "Steward: authorization received. You can close this window.")
			} else {
				_, _ = io.WriteString(w, "Steward: invalid or expired authorization.")
			}
		}
		a.mu.Lock()
		q := r.URL.Query()
		redirect, _ := url.Parse(pending.redirect)
		if a.pending != pending || r.Method != "GET" || r.Host != redirect.Host || r.URL.Path != "/auth/callback" || pending.used || time.Now().After(pending.expires) || len(q["state"]) != 1 || q.Get("state") != pending.state {
			a.mu.Unlock()
			send(400)
			return
		}
		pending.used = true
		client := q.Get("client_id")
		if client == "" && p.Registration != nil {
			client = p.Registration.ClientID
		}
		if q.Has("error") || len(q["code"]) != 1 || q.Get("code") == "" || client == "" || client == "dynamic_agent_client" || len(q["client_id"]) > 1 || p.Registration != nil && client != p.Registration.ClientID {
			pending.status = "failed"
			a.mu.Unlock()
			send(400)
			return
		}
		pending.work.Add(1)
		a.mu.Unlock()
		send(200)
		go func() {
			defer pending.work.Done()
			tokens, err := a.request(work, authURL+"/api/accounts/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {q.Get("code")}, "code_verifier": {pending.verifier}, "redirect_uri": {pending.redirect}, "resource": {apiURL}})
			var credential llm.OAuthCredential
			if err == nil {
				credential, err = a.credential(work, tokens, client, pending.nonce, p.Registration, nil)
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.pending == pending && time.Now().Before(pending.expires) {
				if err != nil {
					pending.status = "failed"
				} else {
					pending.result = &credential
					pending.status = "connected"
				}
			}
		}()
	})}
	pending.server = server
	a.mu.Lock()
	a.pending = pending
	pending.timer = time.AfterFunc(5*time.Minute, func() { a.mu.Lock(); pending.status = "failed"; a.mu.Unlock(); cancel(); _ = server.Close() })
	a.mu.Unlock()
	go func() { _ = server.Serve(listener) }()
	challenge := sha256.Sum256([]byte(pending.verifier))
	clientID := "dynamic_agent_client"
	if p.Registration != nil {
		clientID = p.Registration.ClientID
	}
	q := url.Values{"client_id": {clientID}, "ext_agent_host_id": {"urn:uuid:" + p.DeviceID}, "response_type": {"code"}, "redirect_uri": {pending.redirect}, "resource": {apiURL}, "scope": {accountScope}, "state": {pending.state}, "nonce": {pending.nonce}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "code_challenge_method": {"S256"}}
	if p.Registration == nil {
		q.Set("agent_name_hint", "Steward")
	}
	if p.Credential != nil && p.Credential.IDToken != "" {
		q.Set("id_token_hint", p.Credential.IDToken)
	}
	return llm.AccountLogin{Status: "pending", Events: []llm.AuthEvent{{Kind: "open_url", URL: authURL + "/api/accounts/authorize?" + q.Encode()}}}, nil
}
func (a *accountAuth) status() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil {
		return "logged_out"
	}
	return a.pending.status
}
func (a *accountAuth) poll() (llm.AccountLogin, error) {
	a.mu.Lock()
	p := a.pending
	if p == nil || p.status == "failed" || time.Now().After(p.expires) {
		a.mu.Unlock()
		a.cancel()
		return llm.AccountLogin{}, authError()
	}
	if p.status != "connected" {
		events := []llm.AuthEvent{}
		if p.used && !p.progress {
			events = append(events, llm.AuthEvent{Kind: "progress"})
			p.progress = true
		}
		a.mu.Unlock()
		return llm.AccountLogin{Status: "pending", Events: events}, nil
	}
	result := p.result
	a.mu.Unlock()
	a.cancel()
	return llm.AccountLogin{Status: "connected", Events: []llm.AuthEvent{}, Credential: result}, nil
}
func (a *accountAuth) cancel() {
	a.mu.Lock()
	p := a.pending
	a.pending = nil
	a.mu.Unlock()
	if p != nil {
		if p.timer != nil {
			p.timer.Stop()
		}
		p.cancel()
		_ = p.server.Close()
		p.work.Wait()
	}
}
func (a *accountAuth) refresh(ctx context.Context, raw json.RawMessage) (llm.OAuthCredential, error) {
	var prior llm.OAuthCredential
	if json.Unmarshal(raw, &prior) != nil || prior.ClientID == "" || prior.RefreshToken == "" || prior.SubjectHash == "" {
		return llm.OAuthCredential{}, authError()
	}
	token, err := a.request(ctx, authURL+"/api/accounts/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {prior.ClientID}, "refresh_token": {prior.RefreshToken}, "resource": {apiURL}})
	if err != nil {
		return llm.OAuthCredential{}, err
	}
	return a.credential(ctx, token, prior.ClientID, "", nil, &prior)
}
func (a *accountAuth) credential(ctx context.Context, raw map[string]json.RawMessage, client, nonce string, registration *llm.AccountRegistration, prior *llm.OAuthCredential) (llm.OAuthCredential, error) {
	var token struct {
		Access  string  `json:"access_token"`
		Refresh string  `json:"refresh_token"`
		ID      string  `json:"id_token"`
		Type    string  `json:"token_type"`
		Scope   string  `json:"scope"`
		Expires float64 `json:"expires_in"`
	}
	data, _ := json.Marshal(raw)
	if json.Unmarshal(data, &token) != nil || token.Access == "" || len(token.Access) > 32768 || strings.HasPrefix(token.Access, "sk-") || token.Refresh == "" || len(token.Refresh) > 32768 || !strings.EqualFold(token.Type, "bearer") || token.Expires <= 0 || token.Expires > 31536000 {
		return llm.OAuthCredential{}, authError()
	}
	scopes := strings.Fields(token.Scope)
	subject := ""
	if prior != nil {
		subject = prior.SubjectHash
		if len(scopes) == 0 {
			scopes = prior.Scopes
		}
	}
	valid := false
	for _, scope := range scopes {
		valid = valid || scope == "chatgpt.tokens.use.direct"
	}
	if !valid {
		return llm.OAuthCredential{}, authError()
	}
	if token.ID != "" {
		var err error
		subject, err = a.identity(ctx, token.ID, client, nonce)
		if err != nil {
			return llm.OAuthCredential{}, err
		}
	} else if nonce != "" || subject == "" {
		return llm.OAuthCredential{}, authError()
	}
	if prior != nil && subject != prior.SubjectHash || registration != nil && subject != registration.SubjectHash {
		return llm.OAuthCredential{}, authError()
	}
	id := token.ID
	if id == "" && prior != nil {
		id = prior.IDToken
	}
	return llm.OAuthCredential{ClientID: client, SubjectHash: subject, AccessToken: token.Access, RefreshToken: token.Refresh, IDToken: id, Scopes: scopes, ExpiresAt: time.Now().Add(time.Duration(token.Expires * float64(time.Second)))}, nil
}
func (a *accountAuth) identity(ctx context.Context, token, client, nonce string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 32768 {
		return "", authError()
	}
	decode := base64.RawURLEncoding.DecodeString
	headerRaw, err := decode(parts[0])
	if err != nil {
		return "", authError()
	}
	var header struct {
		Alg, Kid string
		Crit     json.RawMessage
	}
	if json.Unmarshal(headerRaw, &header) != nil || header.Kid == "" || header.Crit != nil || (header.Alg != "RS256" && header.Alg != "ES256") {
		return "", authError()
	}
	jwks, err := a.request(ctx, authURL+"/.well-known/jwks.json", nil)
	if err != nil {
		return "", err
	}
	type jwk struct {
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		Kty string `json:"kty"`
		Crv string `json:"crv"`
		N   string `json:"n"`
		E   string `json:"e"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}
	var keys []jwk
	if json.Unmarshal(jwks["keys"], &keys) != nil || len(keys) > 100 {
		return "", authError()
	}
	matching := []jwk{}
	for _, key := range keys {
		if key.Kid == header.Kid && (key.Use == "" || key.Use == "sig") && (key.Alg == "" || key.Alg == header.Alg) && (header.Alg == "RS256" && key.Kty == "RSA" || header.Alg == "ES256" && key.Kty == "EC" && key.Crv == "P-256") {
			matching = append(matching, key)
		}
	}
	if len(matching) != 1 {
		return "", authError()
	}
	key := matching[0]
	signature, err := decode(parts[2])
	if err != nil {
		return "", authError()
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	verified := false
	if header.Alg == "RS256" && key.Kty == "RSA" {
		n, e := new(big.Int), new(big.Int)
		nb, en := []byte(nil), []byte(nil)
		nb, err = decode(key.N)
		if err == nil {
			en, err = decode(key.E)
		}
		if err == nil {
			n.SetBytes(nb)
			e.SetBytes(en)
			if e.IsInt64() && n.BitLen() >= 2048 {
				verified = rsa.VerifyPKCS1v15(&rsa.PublicKey{N: n, E: int(e.Int64())}, crypto.SHA256, hash[:], signature) == nil
			}
		}
	} else if header.Alg == "ES256" && key.Kty == "EC" && key.Crv == "P-256" && len(signature) == 64 {
		xRaw, e1 := decode(key.X)
		yRaw, e2 := decode(key.Y)
		if e1 == nil && e2 == nil && len(xRaw) == 32 && len(yRaw) == 32 {
			point := append(append([]byte{4}, xRaw...), yRaw...)
			if publicKey, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point); err == nil {
				verified = ecdsa.Verify(publicKey, hash[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:]))
			}
		}
	}
	if !verified {
		return "", authError()
	}
	claimsRaw, err := decode(parts[1])
	if err != nil {
		return "", authError()
	}
	var claims struct {
		Issuer   string          `json:"iss"`
		Subject  string          `json:"sub"`
		Audience json.RawMessage `json:"aud"`
		AZP      string          `json:"azp"`
		Exp      float64         `json:"exp"`
		IAT      float64         `json:"iat"`
		NBF      float64         `json:"nbf"`
		Nonce    string          `json:"nonce"`
	}
	if json.Unmarshal(claimsRaw, &claims) != nil {
		return "", authError()
	}
	now := float64(time.Now().Unix())
	audience := []string{}
	var single string
	if json.Unmarshal(claims.Audience, &single) == nil {
		audience = []string{single}
	} else {
		_ = json.Unmarshal(claims.Audience, &audience)
	}
	aud := false
	for _, v := range audience {
		aud = aud || v == client
	}
	if claims.Issuer != authURL || claims.Subject == "" || !aud || claims.Exp <= now || claims.IAT == 0 || claims.IAT > now+5 || claims.NBF > now+5 || claims.AZP != "" && claims.AZP != client || len(audience) > 1 && claims.AZP != client || nonce != "" && claims.Nonce != nonce {
		return "", authError()
	}
	return digestString(claims.Issuer + "\n" + claims.Subject), nil
}
func (a *accountAuth) revoke(ctx context.Context, raw json.RawMessage) bool {
	var c llm.OAuthCredential
	if json.Unmarshal(raw, &c) != nil || c.RefreshToken == "" {
		return false
	}
	config, err := a.request(ctx, authURL+"/.well-known/openid-configuration", nil)
	if err != nil {
		return false
	}
	var issuer, endpoint string
	_ = json.Unmarshal(config["issuer"], &issuer)
	_ = json.Unmarshal(config["revocation_endpoint"], &endpoint)
	if issuer != authURL {
		return false
	}
	_, err = a.request(ctx, endpoint, url.Values{"token": {c.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {c.ClientID}})
	return err == nil
}
