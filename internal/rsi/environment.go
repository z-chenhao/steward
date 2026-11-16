package rsi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"

	webextract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

type Environment struct {
	Protocol        string `json:"protocol"`
	Platform        string `json:"platform"`
	GoRuntime       string `json:"go_runtime"`
	HostDigest      string `json:"host_digest"`
	ComputeIdentity string `json:"compute_identity,omitempty"`
	ParserIdentity  string `json:"parser_identity,omitempty"`
}

func environment() (Environment, error) {
	out := Environment{Protocol: rsiplugin.Protocol, Platform: runtime.GOOS + "/" + runtime.GOARCH, GoRuntime: runtime.Version()}
	path, err := os.Executable()
	if err != nil {
		return out, err
	}
	file, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, (256<<20)+1))
	if err != nil || n > 256<<20 {
		return out, ErrInvalid
	}
	out.HostDigest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return out, nil
}

// Native compute resources are an independent variation axis. Decisions bind
// their verified identity as well as Host code; drift invalidates activation.
func (h Host) environment(ctx context.Context, g ResolvedGraph) (Environment, error) {
	env, err := environment()
	if err != nil {
		return env, err
	}
	for _, b := range g.Graph.Bindings {
		p, err := h.Catalog.Load(b.PackageDigest)
		if err != nil {
			return env, err
		}
		if hasRequest(p.Manifest, "compute.web-extract") {
			var config webextract.Config
			if rsiplugin.Decode(b.Config, &config) != nil {
				return env, ErrInvalid
			}
			identity := config.RuntimeRevision
			if config.UnavailableError != "" && (identity != webextract.UnavailableRuntime || config.UnavailableError != "WEB_EXTRACTOR_UNAVAILABLE" && config.UnavailableError != "WEB_EXTRACTION_FAILED") {
				return env, ErrInvalid
			}
			if config.UnavailableError != "" {
				identity += ":" + config.UnavailableError
			}
			if config.RuntimeRevision != webextract.UnavailableRuntime {
				resource, err := h.parserProcess().Vault.Load(ctx, config.RuntimeRevision)
				if err != nil {
					return env, err
				}
				identity = resource.Digest()
			}
			if env.ParserIdentity != "" && env.ParserIdentity != identity {
				return env, ErrConflict
			}
			env.ParserIdentity = identity
		}
		if hasRequest(p.Manifest, "compute.ocr") {
			env.ComputeIdentity = "image-ocr-v1:unavailable"
			if h.Compute != nil {
				if h.Compute.Identity == nil {
					return env, ErrInvalid
				}
				env.ComputeIdentity, err = h.Compute.Identity(ctx)
				if err != nil {
					return env, err
				}
			}
		}
	}
	return env, nil
}
