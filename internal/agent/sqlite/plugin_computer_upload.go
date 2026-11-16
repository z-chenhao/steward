package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/z-chenhao/assistant/internal/computer"
	"github.com/z-chenhao/assistant/internal/plugin/computeruse/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/tool"
)

func computerParameterService(ctx context.Context, tx *sql.Tx, runID, method string) (string, bool, error) {
	service := computerParameterContract(method)
	if method == "playwright.upload" {
		var upload bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value=?)`, runID, contract.BrowserUploadContract).Scan(&upload); err != nil {
			return "", false, err
		}
		if upload {
			service = contract.BrowserUploadContract
		}
	}
	var managed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rsi_run r,json_each(r.managed_services_json) s WHERE r.run_id=? AND s.value=?)`, runID, service).Scan(&managed)
	return service, managed, err
}

// The provider port is deliberately one fixed FileChooser operation, not a
// general Driver registry or candidate-supplied program identity/implementation.
type computerUploadResource interface {
	BrowserUploadResource(string) (computer.BrowserUploader, error)
}

func (t *tools) computerBrowserUploader(ctx context.Context, inv tool.Invocation) (computer.BrowserUploader, error) {
	var call computer.BridgeCall
	if json.Unmarshal([]byte(inv.Call.Arguments), &call) != nil {
		return nil, tool.ErrReplay
	}
	if call.Method != "playwright.upload" {
		return nil, nil
	}
	host, revision, err := t.computerMethodHost(ctx, inv, contract.BrowserUploadContract)
	if err != nil || host == nil {
		return nil, err
	}
	return t.bindComputerBrowserUploader(ctx, *host, revision)
}

func (t *tools) bindComputerBrowserUploader(ctx context.Context, host rsi.Host, revision string) (computer.BrowserUploader, error) {
	if err := host.Store.CheckGraph(ctx, revision); err != nil {
		return nil, err
	}
	binding, err := host.ServiceInstance(ctx, revision, contract.BrowserUploadContract)
	if err != nil {
		return nil, err
	}
	var config struct {
		Driver string `json:"browser_upload_driver"`
	}
	if json.Unmarshal(binding.Config, &config) != nil || config.Driver == "" {
		return nil, rsi.ErrInvalid
	}
	provider, ok := t.runs.computerProvider.(computerUploadResource)
	if !ok {
		return nil, rsi.ErrNotFound
	}
	upload, err := provider.BrowserUploadResource(config.Driver)
	if err != nil {
		return nil, errors.Join(rsi.ErrInvalid, err)
	}
	if upload == nil {
		return nil, rsi.ErrNotFound
	}
	return upload, nil
}
