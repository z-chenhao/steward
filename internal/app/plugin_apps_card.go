package app

import (
	"context"
	"github.com/z-chenhao/assistant/internal/interaction/web"
	"github.com/z-chenhao/assistant/internal/plugin/appsinterface/contract"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func attachAppsCardView(ctx context.Context, h rsi.Host, revision string, p web.AppInterfacePolicy, opted, explicit bool) web.AppInterfacePolicy {
	unavailable := web.UnavailableAppCardView(p)
	contribution, err := readAppsViewContribution(ctx, h, revision, contract.CardViewInterface, contract.CardViewResource)
	if err != nil {
		return unavailable
	}
	if !contribution.Provided {
		if !opted || explicit {
			return p
		}
		return unavailable
	}
	var view contract.CardView
	if rsiplugin.DecodeResult(contribution.Output, &view) != nil || view.Resource != contract.CardViewResource || view.Digest != contribution.Digest {
		return unavailable
	}
	result, err := web.ManagedAppCardView(p, view)
	if err != nil {
		return unavailable
	}
	return result
}
