package app

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/connectedapps"
	"github.com/z-chenhao/assistant/internal/plugin/appsaccounts/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (a *App) readAppsAccountPolicies(ctx context.Context) (connectedapps.AccountPolicies, error) {
	return a.appsAccountPoliciesWithHost(ctx, a.Plugins())
}
func (a *App) appsAccountPoliciesWithHost(ctx context.Context, host rsi.Host) (connectedapps.AccountPolicies, error) {
	revision, intentManaged, authManaged, sessionManaged, checkManaged, closeManaged, reconnectManaged, err := a.store.AppsAccountPolicies(ctx)
	if err != nil {
		return connectedapps.AccountPolicies{}, err
	}
	if !intentManaged && !authManaged && !sessionManaged && !checkManaged && !closeManaged && !reconnectManaged {
		return connectedapps.NativeAccountPolicies(), nil
	}
	if revision == "" {
		return connectedapps.AccountPolicies{}, rsi.ErrNotFound
	}
	if err := host.Store.CheckGraph(ctx, revision); err != nil {
		return connectedapps.AccountPolicies{}, err
	}
	selection := connectedapps.NativeAccountPolicies()
	if intentManaged {
		selection = connectedapps.AccountPolicies{}
		if _, err := host.ServiceInstance(ctx, revision, contract.ConnectIntent); err == nil {
			selection = connectedapps.ManagedConnectIntent(func(c context.Context, stateRevision int64, request contract.ConnectRequest, rows []contract.ConnectRow) (contract.ConnectPlan, error) {
				privateHost, err := a.appsPrivateReadHost(host, stateRevision)
				if err != nil {
					return contract.ConnectPlan{}, err
				}
				input, err := json.Marshal(request)
				if err != nil {
					return contract.ConnectPlan{}, err
				}
				raw, err := privateHost.ReadServiceStream(c, revision, contract.ConnectIntent, input, rsi.InputData{ID: contract.ConnectInputID, Data: contract.EncodeConnectRows(rows)})
				if err != nil {
					return contract.ConnectPlan{}, err
				}
				var plan contract.ConnectPlan
				if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
					return contract.ConnectPlan{}, err
				}
				return plan, nil
			})
		}
	}
	auth := a.appsAuthConfigForGraph(ctx, host, revision, authManaged)
	flow := connectedapps.NativeSessionFlow()
	if sessionManaged {
		flow = connectedapps.SessionFlowPolicy{}
		if _, err := host.ServiceInstance(ctx, revision, contract.SessionFlow); err == nil {
			flow = connectedapps.ManagedSessionFlow(func(c context.Context, stateRevision int64, request contract.SessionRequest) (contract.SessionPlan, error) {
				privateHost, err := a.appsPrivateReadHost(host, stateRevision)
				if err != nil {
					return contract.SessionPlan{}, err
				}
				input, err := json.Marshal(request)
				if err != nil {
					return contract.SessionPlan{}, err
				}
				raw, err := privateHost.ReadService(c, revision, contract.SessionFlow, input, nil)
				if err != nil {
					return contract.SessionPlan{}, err
				}
				var plan contract.SessionPlan
				if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
					return contract.SessionPlan{}, err
				}
				return plan, nil
			})
		}
	}
	check := connectedapps.NativeAccountCheck()
	if checkManaged {
		check = connectedapps.AccountCheckPolicy{}
		if _, err := host.ServiceInstance(ctx, revision, contract.AccountCheck); err == nil {
			check = connectedapps.ManagedAccountCheck(func(c context.Context, stateRevision int64, request contract.AccountCheckRequest) (contract.AccountCheckPlan, error) {
				privateHost, err := a.appsPrivateReadHost(host, stateRevision)
				if err != nil {
					return contract.AccountCheckPlan{}, err
				}
				input, err := json.Marshal(request)
				if err != nil {
					return contract.AccountCheckPlan{}, err
				}
				raw, err := privateHost.ReadService(c, revision, contract.AccountCheck, input, nil)
				if err != nil {
					return contract.AccountCheckPlan{}, err
				}
				var plan contract.AccountCheckPlan
				if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
					return contract.AccountCheckPlan{}, err
				}
				return plan, nil
			})
		}
	}
	close, err := a.appsAccountCloseForGraph(ctx, host, revision, closeManaged)
	if err != nil {
		close = connectedapps.AccountClosePolicy{}
	}
	reconnect := connectedapps.NativeReconnect()
	if reconnectManaged {
		reconnect = connectedapps.ReconnectPolicy{}
		if _, err := host.ServiceInstance(ctx, revision, contract.Reconnect); err == nil {
			reconnect = connectedapps.ManagedReconnect(func(c context.Context, stateRevision int64, request contract.ReconnectRequest) (contract.ReconnectPlan, error) {
				privateHost, err := a.appsPrivateReadHost(host, stateRevision)
				if err != nil {
					return contract.ReconnectPlan{}, err
				}
				input, err := json.Marshal(request)
				if err != nil {
					return contract.ReconnectPlan{}, err
				}
				raw, err := privateHost.ReadService(c, revision, contract.Reconnect, input, nil)
				if err != nil {
					return contract.ReconnectPlan{}, err
				}
				var plan contract.ReconnectPlan
				if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
					return contract.ReconnectPlan{}, err
				}
				return plan, nil
			})
		}
	}
	return selection.WithAuthConfig(auth).WithSessionFlow(flow).WithAccountCheck(check).WithAccountClose(close).WithReconnect(reconnect), nil
}

func (a *App) readAppsAccountClose(ctx context.Context) (connectedapps.AccountClosePolicy, error) {
	return a.appsAccountCloseWithHost(ctx, a.Plugins())
}
func (a *App) appsAccountCloseWithHost(ctx context.Context, host rsi.Host) (connectedapps.AccountClosePolicy, error) {
	revision, managed, err := a.store.AppsAccountCloseSelection(ctx)
	if err != nil {
		return connectedapps.AccountClosePolicy{}, err
	}
	return a.appsAccountCloseForGraph(ctx, host, revision, managed)
}
func (a *App) appsAccountCloseForGraph(ctx context.Context, host rsi.Host, revision string, managed bool) (connectedapps.AccountClosePolicy, error) {
	if !managed {
		return connectedapps.NativeAccountClose(), nil
	}
	if _, err := host.ServiceInstance(ctx, revision, contract.AccountClose); err != nil {
		return connectedapps.AccountClosePolicy{}, err
	}
	return connectedapps.ManagedAccountClose(func(c context.Context, stateRevision int64, request contract.AccountCloseRequest) (contract.AccountClosePlan, error) {
		privateHost, err := a.appsPrivateReadHost(host, stateRevision)
		if err != nil {
			return contract.AccountClosePlan{}, err
		}
		input, err := json.Marshal(request)
		if err != nil {
			return contract.AccountClosePlan{}, err
		}
		raw, err := privateHost.ReadService(c, revision, contract.AccountClose, input, nil)
		if err != nil {
			return contract.AccountClosePlan{}, err
		}
		var plan contract.AccountClosePlan
		if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
			return contract.AccountClosePlan{}, err
		}
		return plan, nil
	}), nil
}

func (a *App) readAppsSettings(ctx context.Context) (connectedapps.SettingsPolicy, error) {
	return a.appsSettingsWithHost(ctx, a.Plugins())
}
func (a *App) appsSettingsWithHost(ctx context.Context, host rsi.Host) (connectedapps.SettingsPolicy, error) {
	revision, managed, authManaged, err := a.store.AppsSettingsPolicies(ctx)
	if err != nil {
		return connectedapps.SettingsPolicy{}, err
	}
	if !managed {
		return connectedapps.NativeSettings().WithAuthConfig(a.appsAuthConfigForGraph(ctx, host, revision, authManaged)), nil
	}
	if _, err := host.ServiceInstance(ctx, revision, contract.Settings); err != nil {
		return connectedapps.SettingsPolicy{}, err
	}
	return connectedapps.ManagedSettings(func(c context.Context, stateRevision int64, request contract.SettingsRequest, data []byte) (contract.SettingsPlan, error) {
		privateHost, err := a.appsPrivateReadHost(host, stateRevision)
		if err != nil {
			return contract.SettingsPlan{}, err
		}
		input, err := json.Marshal(request)
		if err != nil {
			return contract.SettingsPlan{}, err
		}
		var raw json.RawMessage
		if request.Mode == "saved" {
			raw, err = privateHost.ReadServiceStream(c, revision, contract.Settings, input, rsi.InputData{ID: contract.SettingsInputID, Data: data})
		} else {
			raw, err = privateHost.ReadService(c, revision, contract.Settings, input, nil)
		}
		if err != nil {
			return contract.SettingsPlan{}, err
		}
		var plan contract.SettingsPlan
		if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
			return contract.SettingsPlan{}, err
		}
		return plan, nil
	}).WithAuthConfig(a.appsAuthConfigForGraph(ctx, host, revision, authManaged)), nil
}

func (a *App) appsAuthConfigForGraph(ctx context.Context, host rsi.Host, revision string, managed bool) connectedapps.AuthConfigPolicy {
	auth := connectedapps.NativeAuthConfig()
	if managed {
		auth = connectedapps.AuthConfigPolicy{}
		if _, err := host.ServiceInstance(ctx, revision, contract.AuthConfig); err == nil {
			auth = connectedapps.ManagedAuthConfig(func(c context.Context, stateRevision int64, request contract.AuthRequest, rows []contract.AuthRow) (contract.AuthPlan, error) {
				privateHost, err := a.appsPrivateReadHost(host, stateRevision)
				if err != nil {
					return contract.AuthPlan{}, err
				}
				input, err := json.Marshal(request)
				if err != nil {
					return contract.AuthPlan{}, err
				}
				raw, err := privateHost.ReadServiceStream(c, revision, contract.AuthConfig, input, rsi.InputData{ID: contract.AuthConfigInputID, Data: contract.EncodeAuthRows(rows)})
				if err != nil {
					return contract.AuthPlan{}, err
				}
				var plan contract.AuthPlan
				if err := rsiplugin.DecodeResult(raw, &plan); err != nil {
					return contract.AuthPlan{}, err
				}
				return plan, nil
			})
		}
	}
	return auth
}
