package sqlite

import (
	"context"
	"encoding/json"

	"github.com/z-chenhao/assistant/internal/plugin/webaccess"
	"github.com/z-chenhao/assistant/internal/rsi"
	"github.com/z-chenhao/assistant/internal/webcontent"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func (t *tools) webRouteContext(ctx context.Context, h rsi.Host, revision string, check func(context.Context) error) (context.Context, error) {
	managed, err := t.runs.managedRunService(ctx, t.claim.RunID(), webaccess.RouteContract)
	if err != nil || !managed {
		return ctx, err
	}
	return webcontent.WithRouteSelector(ctx, func(c context.Context, q webcontent.RouteRequest) (webcontent.RoutePlan, error) {
		var plan webcontent.RoutePlan
		if err := check(c); err != nil {
			return plan, err
		}
		input, err := json.Marshal(q)
		if err != nil {
			return plan, err
		}
		raw, err := h.ReadService(c, revision, webaccess.RouteContract, input, nil)
		t.runs.host.boundary("after_web_route_method")
		if authority := check(c); authority != nil {
			return plan, authority
		}
		if authority := h.Store.CheckGraph(c, revision); authority != nil {
			return plan, authority
		}
		if err == nil && rsiplugin.DecodeResult(raw, &plan) != nil {
			err = rsi.ErrInvalid
		}
		return plan, err
	}), nil
}
