package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func attachAppsSidebarView(ctx context.Context, h rsi.Host, revision string, p web.AppInterfacePolicy, opted, explicit bool) web.AppInterfacePolicy {
	unavailable := web.UnavailableAppSidebarView(p)
	contribution, err := readAppsViewContribution(ctx, h, revision, contract.SidebarViewInterface, contract.SidebarViewResource)
	if err != nil {
		return unavailable
	}
	if !contribution.Provided {
		if !opted || explicit {
			return p
		}
		return unavailable
	}
	var view contract.SidebarView
	if rsiplugin.DecodeResult(contribution.Output, &view) != nil || view.Resource != contract.SidebarViewResource || view.Digest != contribution.Digest {
		return unavailable
	}
	result, err := web.ManagedAppSidebarView(p, view)
	if err != nil {
		return unavailable
	}
	return result
}
