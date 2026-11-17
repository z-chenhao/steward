package web

import (
	"context"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/appslogos/artwork"
	"time"
)

// Exact pre-logo/default23/direct compatibility. New managed reads never call
// this path on failure. Delete when these documented native consumers retire.
func (c *appLogoCache) getLegacyV1(ctx context.Context, slug string) ([]byte, string, error) {
	c.mu.Lock()
	source := c.sources[slug]
	if source == "" {
		source = logoSource(slug, "")
	}
	key := slug + source
	if v, ok := c.entries[key]; ok && time.Since(v.at) < 24*time.Hour {
		c.mu.Unlock()
		return v.body, v.kind, nil
	}
	c.mu.Unlock()
	// Recommended apps are bundled from this same official API for offline
	// startup. New apps use the provider's logo URL through the same boundary.
	body, err := artwork.Read(slug)
	if err != nil {
		c.mu.Lock()
		registered := c.sources[slug] != ""
		c.mu.Unlock()
		if !registered {
			return nil, "", errors.New("unknown app logo")
		}
		body, err = c.fetchAppLogo(ctx, source)
	}
	if err != nil {
		return nil, "", err
	}
	body, kind, err := validateAppLogo(body)
	if err != nil {
		return nil, "", err
	}
	c.storeLogo(key, body, kind)
	return body, kind, nil
}
