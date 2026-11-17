package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/z-chenhao/assistant/internal/connector/composio"
)

const maxAppLogo = 512 << 10

type cachedAppLogo struct {
	body []byte
	kind string
	at   time.Time
}
type appLogoCache struct {
	mu      sync.Mutex
	sources map[string]string
	entries map[string]cachedAppLogo
	client  *http.Client // protocol peer injection; never supplied by a request
}

func logoSource(slug, raw string) string {
	u, err := url.Parse(raw)
	if err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && u.RawQuery == "" && u.Fragment == "" && (u.Hostname() == "logos.composio.dev" || u.Hostname() == "assets.composio.dev") {
		return u.String()
	}
	return "https://logos.composio.dev/api/" + url.PathEscape(slug)
}
func (c *appLogoCache) registerForRevision(revision, slug, source string) {
	if !composio.ValidID(slug) {
		return
	}
	key := logoSourceKey(revision, slug)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sources == nil {
		c.sources = map[string]string{}
	}
	if source != "" || c.sources[key] == "" {
		if len(c.sources) < 4096 || c.sources[key] != "" {
			c.sources[key] = logoSource(slug, source)
		}
	}
}
func (c *appLogoCache) fetchAppLogo(ctx context.Context, source string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, "GET", source, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "image/svg+xml,image/png,image/jpeg")
	client := http.Client{Timeout: 8 * time.Second}
	if c.client != nil {
		client = *c.client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("app logo unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("app logo unavailable")
	}
	return io.ReadAll(io.LimitReader(response.Body, maxAppLogo+1))
}
func validateAppLogo(body []byte) ([]byte, string, error) {
	if len(body) > maxAppLogo {
		return nil, "", errors.New("app logo unavailable")
	}
	kind := "image/svg+xml"
	if config, format, e := image.DecodeConfig(bytes.NewReader(body)); e == nil {
		if config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 4_194_304 || (format != "png" && format != "jpeg") {
			return nil, "", errors.New("invalid app logo")
		}
		kind = "image/" + format
	} else {
		var err error
		body, err = cleanAppSVG(body)
		if err != nil {
			return nil, "", err
		}
	}
	return body, kind, nil
}
func (c *appLogoCache) storeLogo(key string, body []byte, kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cachedAppLogo{}
	}
	if len(c.entries) >= 128 {
		for old := range c.entries {
			delete(c.entries, old)
			break
		}
	}
	c.entries[key] = cachedAppLogo{body: body, kind: kind, at: time.Now()}
}

func (s *Server) appLogo(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/apps/logo/")
	if !composio.ValidID(slug) {
		fail(w, 404)
		return
	}
	policy := NativeAppLogos()
	revision := r.URL.Query().Get("revision")
	var err error
	if s.Logos != nil {
		policy, err = s.Logos(r.Context(), revision)
	} else if revision != "" {
		err = errors.New("app logo unavailable")
	}
	if err != nil {
		fail(w, 404)
		return
	}
	var body []byte
	var kind string
	seconds := 86400
	if policy.native {
		body, kind, err = s.appLogos.getLegacyV1(r.Context(), slug)
	} else {
		body, kind, seconds, err = s.appLogos.getManaged(r.Context(), slug, policy)
	}
	if err != nil {
		fail(w, 404)
		return
	}
	hash := sha256.Sum256(body)
	w.Header().Set("ETag", `"`+hex.EncodeToString(hash[:])+`"`)
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "private, max-age="+strconv.Itoa(seconds))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(body)
}

var localSVGReference = regexp.MustCompile(`^url\(#[A-Za-z0-9_.:-]+\)$`)

func safeSVGValue(value string) bool {
	v := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(v, "url(") {
		return localSVGReference.MatchString(value)
	}
	return !strings.ContainsAny(v, "\\<>") && !strings.Contains(v, "http:") && !strings.Contains(v, "https:") && !strings.Contains(v, "javascript:") && !strings.Contains(v, "data:") && !strings.Contains(v, "expression(")
}
func cleanAppSVG(body []byte) ([]byte, error) {
	allowedTags := strings.Fields("svg g path defs clipPath rect circle ellipse line polygon polyline linearGradient radialGradient stop mask pattern filter feFlood feBlend feGaussianBlur feOffset feComposite feColorMatrix feMerge feMergeNode title desc use")
	allowedAttrs := strings.Fields("viewBox width height fill fill-rule fill-opacity stroke stroke-width stroke-linecap stroke-linejoin stroke-opacity stroke-miterlimit clip-rule clip-path opacity d points x y x1 x2 y1 y2 cx cy r rx ry transform id offset stop-color stop-opacity gradientUnits gradientTransform maskUnits maskContentUnits mask-type patternUnits patternContentUnits patternTransform filterUnits primitiveUnits stdDeviation in in2 mode result color-interpolation-filters dx dy type values preserveAspectRatio")
	tags, attrs := map[string]bool{}, map[string]bool{}
	for _, name := range allowedTags {
		tags[name] = true
	}
	for _, name := range allowedAttrs {
		attrs[name] = true
	}
	decoder := xml.NewDecoder(bytes.NewReader(body))
	var output bytes.Buffer
	encoder := xml.NewEncoder(&output)
	depth, skip, count := 0, 0, 0
	rootSeen := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("invalid app logo")
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			count++
			if depth == 1 {
				if rootSeen || value.Name.Local != "svg" {
					return nil, errors.New("invalid app logo")
				}
				rootSeen = true
			}
			if depth > 64 || count > 20000 {
				return nil, errors.New("invalid app logo")
			}
			if skip > 0 {
				skip++
				continue
			}
			if !tags[value.Name.Local] {
				skip = 1
				continue
			}
			name := value.Name.Local
			value.Name = xml.Name{Local: name}
			clean := []xml.Attr{}
			if depth == 1 {
				clean = append(clean, xml.Attr{Name: xml.Name{Local: "xmlns"}, Value: "http://www.w3.org/2000/svg"})
			}
			for _, attr := range value.Attr {
				key := attr.Name.Local
				if key == "href" && strings.HasPrefix(attr.Value, "#") && !strings.ContainsAny(attr.Value, " /\\") {
					clean = append(clean, xml.Attr{Name: xml.Name{Local: "href"}, Value: attr.Value})
					continue
				}
				if key == "style" {
					for _, rule := range strings.Split(attr.Value, ";") {
						key, v, ok := strings.Cut(rule, ":")
						key = strings.TrimSpace(key)
						v = strings.TrimSpace(v)
						if ok && attrs[key] && safeSVGValue(v) {
							clean = append(clean, xml.Attr{Name: xml.Name{Local: key}, Value: v})
						}
					}
					continue
				}
				if attrs[key] && safeSVGValue(attr.Value) {
					clean = append(clean, xml.Attr{Name: xml.Name{Local: key}, Value: attr.Value})
				}
			}
			// Presentation styles override attributes without producing duplicate XML attributes.
			dedup := make([]xml.Attr, 0, len(clean))
			positions := map[string]int{}
			for _, attr := range clean {
				if index, ok := positions[attr.Name.Local]; ok {
					dedup[index] = attr
				} else {
					positions[attr.Name.Local] = len(dedup)
					dedup = append(dedup, attr)
				}
			}
			value.Attr = dedup
			if err = encoder.EncodeToken(value); err != nil {
				return nil, err
			}
		case xml.EndElement:
			depth--
			if skip > 0 {
				skip--
				continue
			}
			value.Name = xml.Name{Local: value.Name.Local}
			if err = encoder.EncodeToken(value); err != nil {
				return nil, err
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return nil, errors.New("invalid app logo")
			}
			if skip == 0 {
				if err = encoder.EncodeToken(value); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := encoder.Flush(); err != nil {
		return nil, err
	}
	if !rootSeen || output.Len() == 0 || depth != 0 {
		return nil, errors.New("invalid app logo")
	}
	return output.Bytes(), nil
}
