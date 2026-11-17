package webaccess

import (
	"context"
	"strings"

	"github.com/z-chenhao/assistant/internal/webcontent"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

const RouteContract = "steward.web-content-route/1"

// This method owns the format/mode recipe; Host owns compatible codec execution,
// original bytes, limits, source facts and current accepted Tool authority.
func routeService(ctx context.Context, call rsiplugin.Call) (rsiplugin.Result, error) {
	var q webcontent.RouteRequest
	if err := rsiplugin.Decode(call.Input, &q); err != nil {
		return rsiplugin.Result{}, err
	}
	return rsiplugin.JSONResult(ctx, contentRoute(q))
}
func contentRoute(q webcontent.RouteRequest) webcontent.RoutePlan {
	reject := webcontent.RoutePlan{ErrorCode: "WEB_UNSUPPORTED_CONTENT"}
	if q.Operation == "fetch" {
		switch q.MediaType {
		case "text/html":
			return webcontent.RoutePlan{Decoder: "html"}
		case "text/plain", "text/markdown":
			return webcontent.RoutePlan{Decoder: "text"}
		}
		return reject
	}
	if strings.HasPrefix(q.MediaType, "image/") {
		if q.Mode == "raw" || q.Mode == "answer" {
			return reject
		}
		return webcontent.RoutePlan{Decoder: "image"}
	}
	switch q.MediaType {
	case "text/html":
		if q.Mode == "raw" {
			return webcontent.RoutePlan{Decoder: "text"}
		}
		if q.DocumentAvailable {
			return webcontent.RoutePlan{Decoder: "document"}
		}
		return webcontent.RoutePlan{Decoder: "html"}
	case "application/pdf":
		if q.Mode == "raw" {
			return reject
		}
		if !q.DocumentAvailable {
			return webcontent.RoutePlan{ErrorCode: "WEB_EXTRACTOR_UNAVAILABLE"}
		}
		return webcontent.RoutePlan{Decoder: "document"}
	case "text/plain", "text/markdown":
		return webcontent.RoutePlan{Decoder: "text"}
	}
	return reject
}
