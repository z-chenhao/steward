package intelligentui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/delivery/uidocv1"
	"github.com/z-chenhao/assistant/internal/plugin/intelligentui/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

// Only two fixed files beside this executable are read. The caller supplies no
// path, credential, row or authority; Host must match names and digests to the
// exact selected package's declared, sealed resources before using any bytes.
func definition(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var request struct{}
	if err := rsiplugin.Decode(call.Input, &request); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(call.Inputs) != 0 {
		return rsiplugin.Result{}, errors.New("unexpected UI definition input")
	}
	runtime, err := ownViewDigest(contract.RuntimeFile)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	styles, err := ownViewDigest(contract.StylesFile)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contract.Definition{InputSchema: uidocv1.ShowSchema, ModelInputSchema: uidocv1.ModelShowSchema, Description: "Show visuals; read intelligent_ui. text precedes a visual; replace corrects a staged ui_ref. Staged is success.", Runtime: contract.ViewResource{Name: contract.RuntimeResource, Digest: runtime}, Styles: contract.ViewResource{Name: contract.StylesResource, Digest: styles}})
}

func ownViewDigest(name string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(filepath.Join(filepath.Dir(executable), name))
	if err != nil {
		return "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || len(raw) > 32<<20 {
		return "", errors.New("invalid UI view resource")
	}
	return rsiplugin.FileDigest(raw), nil
}
