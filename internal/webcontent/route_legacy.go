package webcontent

import "strings"

// Exact pre-route-service policy: original seven-service and earlier graphs,
// native Runs and explicit unbundled or previously selected nineteen-package carriers. Never a managed error
// fallback. Delete once no retained Run/reference or documented carrier needs it.
func routeLegacyV1(q RouteRequest) RoutePlan {
	reject := RoutePlan{ErrorCode: "WEB_UNSUPPORTED_CONTENT"}
	if q.Operation == "fetch" {
		switch q.MediaType {
		case "text/html":
			return RoutePlan{Decoder: "html"}
		case "text/plain", "text/markdown":
			return RoutePlan{Decoder: "text"}
		}
		return reject
	}
	if strings.HasPrefix(q.MediaType, "image/") {
		if q.Mode == "raw" || q.Mode == "answer" {
			return reject
		}
		return RoutePlan{Decoder: "image"}
	}
	switch q.MediaType {
	case "text/html":
		if q.Mode == "raw" {
			return RoutePlan{Decoder: "text"}
		}
		if q.DocumentAvailable {
			return RoutePlan{Decoder: "document"}
		}
		return RoutePlan{Decoder: "html"}
	case "application/pdf":
		if q.Mode == "raw" {
			return reject
		}
		if !q.DocumentAvailable {
			return RoutePlan{ErrorCode: "WEB_EXTRACTOR_UNAVAILABLE"}
		}
		return RoutePlan{Decoder: "document"}
	case "text/plain", "text/markdown":
		return RoutePlan{Decoder: "text"}
	}
	return reject
}
