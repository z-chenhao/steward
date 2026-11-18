package v1

import (
	_ "embed"
	"github.com/z-chenhao/assistant/internal/plugin/capabilityforms/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed view/capabilities.html
var viewSource string

//go:embed view/copy.json
var copySource string

func assets(out *contract.Result) {
	out.View = &contract.Asset{Resource: contract.ViewResource, Digest: rsiplugin.FileDigest([]byte(viewSource)), Source: viewSource}
	out.Copy = &contract.Asset{Resource: contract.CopyResource, Digest: rsiplugin.FileDigest([]byte(copySource)), Source: copySource}
}
