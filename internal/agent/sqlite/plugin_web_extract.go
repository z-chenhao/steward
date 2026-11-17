package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/z-chenhao/assistant/internal/agent"
	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	webextract "github.com/z-chenhao/assistant/internal/plugin/webextract/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
	"github.com/z-chenhao/assistant/internal/webcontent"
)

var errWebExtractMethod = errors.New("managed document decoder unavailable")

func webToolName(name string) bool {
	return name == webaccess.SearchName || name == webaccess.FetchName || name == webaccess.ContentName || name == webaccess.CheckName || name == webaccess.ReadName
}
func (r *Runs) webResourceIdentity(run agent.Run, name string) string {
	if strings.HasPrefix(runWebResource(run, name), "web-access-v3:") {
		return r.webAccess.ScopeIdentity()
	}
	return r.webAccess.Identity()
}

// A managed decoder pins interpreter/module bytes in the graph. The live
// configuration remains the authority for endpoints, credentials and source
// selection; replacing installation bytes alone must not rewrite an old Run.
func (r *Runs) webRegistryForRun(ctx context.Context, c agent.RunClaim, base *tool.Registry) (*tool.Registry, error) {
	hasWeb := false
	for _, spec := range base.Describe() {
		if webToolName(spec.Name) {
			hasWeb = true
			break
		}
	}
	if !hasWeb {
		return base, nil
	}
	managed, err := r.managedRunService(ctx, c.RunID(), webextract.Contract)
	if err != nil || !managed {
		return base, err
	}
	definitions := []tool.Definition{}
	for _, spec := range base.Describe() {
		definition, _ := base.Resolve(spec.Name)
		if webToolName(spec.Name) {
			definition.Spec.ResourceHash = r.webAccess.ScopeIdentity()
			for i := range definition.PreviousSpecs {
				definition.PreviousSpecs[i].ResourceHash = definition.Spec.ResourceHash
			}
		}
		definitions = append(definitions, definition)
	}
	return tool.NewRegistry(definitions)
}
func (t *tools) webExtractContext(ctx context.Context, h rsi.Host, revision string, check func(context.Context) error) (context.Context, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), webextract.Contract)
	if err != nil || !managed {
		return ctx, err
	}
	if err := check(ctx); err != nil {
		return ctx, err
	}
	binding, err := h.ServiceInstance(ctx, revision, webextract.Contract)
	var config webextract.Config
	if err == nil && (json.Unmarshal(binding.Config, &config) != nil || !t.runs.webAccess.MatchesParserSource(config.SourceIdentity)) {
		err = rsi.ErrRevoked
	}
	if err != nil {
		return ctx, errors.Join(errWebExtractMethod, err)
	}
	configured := config.RuntimeRevision != webextract.UnavailableRuntime || t.runs.webAccess.ExtractScript != ""
	return webcontent.WithDocumentParser(ctx, configured, func(c context.Context, url, kind string, data []byte) (string, string, error) {
		if err := check(c); err != nil {
			return "", "", err
		}
		response, err := h.ExtractService(c, revision, webextract.Request{InputID: "document", URL: url, MediaType: kind}, data, check)
		t.runs.host.boundary("after_web_extract_method")
		if authority := check(c); authority != nil {
			return "", "", errors.Join(errWebExtractMethod, authority)
		}
		if authority := h.Store.CheckGraph(c, revision); authority != nil {
			return "", "", errors.Join(errWebExtractMethod, authority)
		}
		if err != nil {
			if config.RuntimeRevision == webextract.UnavailableRuntime && (err.Error() == "WEB_EXTRACTOR_UNAVAILABLE" || err.Error() == "WEB_EXTRACTION_FAILED") {
				return "", "", err
			}
			return "", "", errors.Join(errWebExtractMethod, err)
		}
		return response.Title, response.Text, nil
	}), nil
}
