package appsinterface

import (
	"context"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func compileAccountComposition(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	locale, err := presentationLocale(call)
	if err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contract.AccountComposition{
		Composition: localizedComposition(locale),
		AccountCopy: accountCopy(locale),
	})
}

func accountCopy(locale string) contract.AccountCopy {
	return contract.AccountCopy{
		Reconnect:             localizedText(locale, "Reconnect"),
		RefreshStatus:         localizedText(locale, "Refresh status"),
		Disconnect:            localizedText(locale, "Disconnect"),
		ConfirmDisconnect:     localizedText(locale, "Disconnect this account?"),
		DisconnectDescription: localizedText(locale, "Ghost will stop using this account. Your other accounts stay connected."),
		OpenAuthorization:     localizedText(locale, "Open authorization page"),
	}
}
