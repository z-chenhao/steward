package computer

import (
	"context"
	"github.com/z-chenhao/assistant/internal/tool"
)

type DocumentationReader func(context.Context, string) (string, error)

// DocumentationReceipt preserves the original lease through the Tool commit.
// It grants no reference, does not update observations, and is never serialized.
type DocumentationReceipt struct {
	Lease    string
	Instance Instance
}

func (r UseRuntime) documentation(ctx context.Context, kind string) (tool.Result, error) {
	if r.ReadDocumentation != nil {
		text, err := r.ReadDocumentation(ctx, kind)
		if err != nil {
			return tool.Result{}, err
		}
		result := useResult(text)
		result.HostFence = DocumentationReceipt{Lease: r.Lease, Instance: r.Instance}
		return result, nil
	}
	// Only pre-contract Runs and explicit unbundled carriers retain facade v1.
	switch kind {
	case "core":
		return useResult(nativeCoreDocumentationV1), nil
	case "browser":
		return useResult(nativeBrowserDocumentationV1), nil
	}
	return tool.Failure("INVALID_ARGUMENT"), nil
}
