package webcontent

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

var errBlocked = errors.New("WEB_ADDRESS_BLOCKED")
var reserved = func() []netip.Prefix {
	out := []netip.Prefix{}
	for _, s := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "::/96", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "ff00::/8"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func publicIP(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.Is6() && (!netip.MustParsePrefix("2000::/3").Contains(a) || netip.MustParsePrefix("3fff::/20").Contains(a)) {
		return false
	}
	for _, p := range reserved {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// ValidURL checks syntax and literal addresses. DNS answers are additionally
// checked and pinned by the dialer, on every connection including redirects.
func ValidURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 4096 || !utf8.ValidString(raw) || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.Contains(u.Hostname(), "%") {
		return errBlocked
	}
	if u.Port() != "" && u.Port() != "80" && u.Port() != "443" {
		return errBlocked
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return errBlocked
	}
	if ip, e := netip.ParseAddr(host); e == nil && !publicIP(ip) {
		return errBlocked
	}
	return nil
}
func publicClient() *http.Client {
	tr := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true}
	tr.DialContext = publicDialContext(net.DefaultResolver.LookupNetIP, (&net.Dialer{Timeout: 5 * time.Second}).DialContext)
	return &http.Client{Transport: tr, Timeout: 25 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("WEB_REDIRECT_LIMIT")
		}
		return ValidURL(req.URL.String())
	}}
}

func publicDialContext(lookup func(context.Context, string, string) ([]netip.Addr, error), dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, errBlocked
		}
		ips, e := lookup(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, errBlocked
		}
		for i := range ips {
			// net.Resolver may represent ordinary A records as IPv4-mapped
			// addresses. Normalize DNS answers before public-range validation;
			// mapped literals supplied in a URL remain rejected by ValidURL.
			ips[i] = ips[i].Unmap()
			ip := ips[i]
			if !publicIP(ip) {
				return nil, errBlocked
			}
		}
		var last error
		for _, ip := range ips {
			conn, e := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
}

type Page struct {
	MediaType   string `json:"media_type,omitempty"`
	Image       *Image `json:"image,omitempty"`
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Text        string `json:"text"`
	RetrievedAt string `json:"retrieved_at"`
	Freshness   string `json:"freshness"`
	Untrusted   bool   `json:"untrusted"`
}

func Fetch(ctx context.Context, raw string) (Page, error) { return fetch(ctx, raw, publicClient()) }
func fetch(ctx context.Context, raw string, client *http.Client) (Page, error) {
	p := Page{URL: raw, Freshness: "fetched_now; origin may serve cached content", Untrusted: true}
	if err := ValidURL(raw); err != nil {
		return p, err
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return p, errBlocked
	}
	req.Header.Set("User-Agent", "Steward-WebAccess/1.0")
	req.Header.Set("Accept", "text/html, text/plain, text/markdown;q=0.9")
	res, e := client.Do(req)
	if e != nil {
		if errors.Is(e, errBlocked) {
			return p, errBlocked
		}
		return p, errors.New("WEB_FETCH_FAILED")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return p, errors.New("WEB_LOGIN_OR_ACCESS_REQUIRED")
	}
	if res.StatusCode != 200 {
		return p, errors.New("WEB_HTTP_ERROR")
	}
	media, _, e := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if e != nil {
		return p, errors.New("WEB_UNSUPPORTED_CONTENT")
	}
	if media != "text/html" && media != "text/plain" && media != "text/markdown" {
		return p, errors.New("WEB_UNSUPPORTED_CONTENT")
	}
	body, e := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if e != nil || len(body) > 2<<20 {
		return p, errors.New("WEB_BODY_LIMIT")
	}
	p.URL = res.Request.URL.String()
	decoded, e := DecodePage(ctx, Config{}, Page{URL: p.URL, MediaType: media}, "fetch", "", body)
	if e != nil {
		return p, e
	}
	p.Title, p.Text = decoded.Title, decoded.Text
	p.RetrievedAt = time.Now().UTC().Format(time.RFC3339)
	return p, nil
}

func Preview(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}
