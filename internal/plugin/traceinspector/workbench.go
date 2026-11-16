package traceinspector

import (
	"context"
	_ "embed"
	"errors"

	"github.com/z-chenhao/assistant/internal/plugin/traceinspector/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

//go:embed view/trace-worker.js
var workbenchProgramSource string

//go:embed view/trace-activity.css
var workbenchStylesheetSource string

//go:embed view/trace.html
var workbenchTemplateSource string

//go:embed view/copy.json
var workbenchCopySource string

// Workbench resources require no ledger facts or model bodies. The authenticated
// reader freezes and verifies the package resource before exposing the Worker.
func workbench(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request contract.WorkbenchRequest
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errors.New("unexpected workbench facts")
	}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contract.WorkbenchResult{Copy: contract.Template{Resource: contract.CopyResource, Digest: rsiplugin.FileDigest([]byte(workbenchCopySource)), Source: workbenchCopySource}, Template: contract.Template{Resource: contract.TemplateResource, Digest: rsiplugin.FileDigest([]byte(workbenchTemplateSource)), Source: workbenchTemplateSource}, Program: contract.BrowserProgram{Resource: contract.ProgramResource, Digest: rsiplugin.FileDigest([]byte(workbenchProgramSource)), Source: workbenchProgramSource}, Stylesheet: contract.Stylesheet{Resource: contract.StylesheetResource, Digest: rsiplugin.FileDigest([]byte(workbenchStylesheetSource)), Source: workbenchStylesheetSource}})
}
