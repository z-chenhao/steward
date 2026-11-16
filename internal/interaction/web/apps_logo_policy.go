package web

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/z-chenhao/assistant/internal/plugin/appslogos/contract"
)

// AppLogoReader is the owning Web consumer's private, exact-graph seam. The
// candidate sees public slug/cache facts, never a URL, credential or Web model.
type AppLogoReader func(context.Context, string) (AppLogoPolicy, error)
type AppLogoPolicy struct {
	native   bool
	revision string
	read     func(context.Context, contract.Request) (contract.Plan, error)
	check    func(context.Context) error
}

type appLogoPolicyKey struct{}

// Preserve one request's original resource interpretation across its accepted
// account effect. A later graph must not replace the response's image references.
func (s *Server) appLogoPolicy(ctx context.Context) AppLogoPolicy {
	if value, ok := ctx.Value(appLogoPolicyKey{}).(AppLogoPolicy); ok {
		return value
	}
	if s.Logos == nil {
		return NativeAppLogos()
	}
	value, err := s.Logos(ctx, "")
	if err != nil {
		return AppLogoPolicy{}
	}
	return value
}

func NativeAppLogos() AppLogoPolicy { return AppLogoPolicy{native: true} }
func ManagedAppLogos(revision string, read func(context.Context, contract.Request) (contract.Plan, error), check func(context.Context) error) AppLogoPolicy {
	return AppLogoPolicy{revision: revision, read: read, check: check}
}
func logoSourceKey(revision, slug string) string {
	if revision == "" {
		return slug
	}
	return revision + "|" + slug
}
func (c *appLogoCache) getManaged(ctx context.Context, slug string, p AppLogoPolicy) ([]byte, string, int, error) {
	unavailable := errors.New("app logo unavailable")
	if p.revision == "" || p.read == nil || p.check == nil {
		return nil, "", 0, unavailable
	}
	c.mu.Lock()
	source := c.sources[logoSourceKey(p.revision, slug)]
	key := p.revision + "|" + slug + source
	value, cached := c.entries[key]
	c.mu.Unlock()
	request := contract.Request{Slug: slug, Registered: source != "", Cached: cached}
	if cached {
		request.AgeNS = int64(time.Since(value.at))
	}
	plan, err := p.read(ctx, request)
	if err != nil || plan.CacheSeconds < 0 || plan.CacheSeconds > 86400 {
		return nil, "", 0, unavailable
	}
	var body []byte
	switch plan.Kind {
	case "cached":
		if !cached || time.Since(value.at) >= 24*time.Hour || len(plan.Body) != 0 {
			return nil, "", 0, unavailable
		}
		if err := p.check(ctx); err != nil {
			return nil, "", 0, unavailable
		}
		return value.body, value.kind, plan.CacheSeconds, nil
	case "bundled":
		body = plan.Body
	case "provider":
		if source == "" || len(plan.Body) != 0 {
			return nil, "", 0, unavailable
		}
		body, err = c.fetchAppLogo(ctx, source)
		if err != nil {
			return nil, "", 0, err
		}
	default:
		return nil, "", 0, unavailable
	}
	body, kind, err := validateAppLogo(body)
	if err != nil {
		return nil, "", 0, err
	}
	if err := p.check(ctx); err != nil {
		return nil, "", 0, unavailable
	}
	if plan.CacheSeconds > 0 {
		c.storeLogo(key, body, kind)
	}
	return body, kind, plan.CacheSeconds, nil
}

// Mutable original /logo/slug URLs keep their old behavior. New page URLs name
// the selected graph; cache/source facts cannot leak across independent versions.
// Bundled resources survive a cold restart in their retained package. Unknown
// provider metadata has only the original in-memory registration lifetime.
func (s *Server) projectAppLogos(view *appsView, p AppLogoPolicy) {
	apply := func(card *appCard) {
		if !p.native && (p.revision == "" || p.read == nil || p.check == nil) {
			card.Icon = ""
			return
		}
		s.appLogos.registerForRevision(p.revision, card.Slug, card.Meta.Logo)
		card.Icon = "/apps/logo/" + card.Slug
		if p.revision != "" {
			card.Icon += "?revision=" + url.QueryEscape(p.revision)
		}
	}
	for _, card := range appViewCards(view) {
		apply(card)
	}
}
