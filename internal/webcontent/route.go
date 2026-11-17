package webcontent

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// These consumer DTOs expose format/intent facts only. They carry no URL,
// source bytes, config, credentials, operation identity or executable choice.
type RouteRequest struct {
	Operation         string `json:"operation"`
	MediaType         string `json:"media_type"`
	Mode              string `json:"mode"`
	DocumentAvailable bool   `json:"document_available"`
}
type RoutePlan struct {
	Decoder   string `json:"decoder"`
	ErrorCode string `json:"error_code"`
}
type RouteSelector func(context.Context, RouteRequest) (RoutePlan, error)
type routeSelectorKey struct{}

var ErrRouteUnavailable = errors.New("managed content route unavailable")

func WithRouteSelector(ctx context.Context, selectRoute RouteSelector) context.Context {
	return context.WithValue(ctx, routeSelectorKey{}, selectRoute)
}

// validRoute checks codec compatibility and explicit raw/image intent. It does
// not choose a decoder or copy the plugin's default routing recipe.
func validRoute(q RouteRequest, p RoutePlan) bool {
	if p.ErrorCode != "" {
		return p.Decoder == "" && (p.ErrorCode == "WEB_UNSUPPORTED_CONTENT" ||
			p.ErrorCode == "WEB_EXTRACTOR_UNAVAILABLE" && q.Operation == "content" && q.MediaType == "application/pdf" && q.Mode != "raw" && !q.DocumentAvailable)
	}
	text := q.MediaType == "text/html" || q.MediaType == "text/plain" || q.MediaType == "text/markdown"
	if q.Operation == "fetch" {
		return text && (p.Decoder == "text" || p.Decoder == "html" && q.MediaType == "text/html")
	}
	if q.Mode == "raw" {
		return text && p.Decoder == "text"
	}
	switch p.Decoder {
	case "text":
		return text
	case "html":
		return q.MediaType == "text/html"
	case "document":
		return q.DocumentAvailable && (q.MediaType == "text/html" || q.MediaType == "application/pdf")
	case "image":
		return strings.HasPrefix(q.MediaType, "image/") && q.Mode != "answer"
	}
	return false
}

// DecodePage is the shared post-transport decoder for the two existing page
// operations. Original URL/media/time/untrusted facts remain untouched. The
// selector can only choose existing compatible codecs, never perform IO.
func DecodePage(ctx context.Context, c Config, page Page, operation, mode string, data []byte) (Page, error) {
	if operation != "fetch" && operation != "content" || mode != "" && mode != "readable" && mode != "raw" && mode != "answer" || operation == "fetch" && mode != "" {
		return page, errors.New("INVALID_ARGUMENT")
	}
	if operation == "fetch" && len(data) > 2<<20 {
		return page, errors.New("WEB_BODY_LIMIT")
	}
	if len(data) > 8<<20 {
		return page, errors.New("RESOURCE_LIMIT")
	}
	decoder, bound := ctx.Value(documentParserKey{}).(documentDecoder)
	q := RouteRequest{Operation: operation, MediaType: page.MediaType, Mode: mode, DocumentAvailable: bound && decoder.configured || c.ExtractScript != ""}
	var plan RoutePlan
	if selector, managed := ctx.Value(routeSelectorKey{}).(RouteSelector); managed {
		var err error
		plan, err = selector(ctx, q)
		if err != nil {
			return page, errors.Join(ErrRouteUnavailable, err)
		}
		if !validRoute(q, plan) {
			return page, ErrRouteUnavailable
		}
		// Opting into managed routing cannot reopen a legacy algorithm when a
		// required decoder is absent from the original frozen composition.
		_, htmlBound := ctx.Value(htmlParserKey{}).(HTMLParser)
		_, imageBound := ctx.Value(imageParserKey{}).(ImageParser)
		if plan.Decoder == "document" && !bound || plan.Decoder == "html" && !htmlBound || plan.Decoder == "image" && !imageBound {
			return page, ErrRouteUnavailable
		}
	} else {
		plan = routeLegacyV1(q)
	}
	if plan.ErrorCode != "" {
		return page, errors.New(plan.ErrorCode)
	}
	if plan.Decoder != "image" && (plan.Decoder != "document" || q.MediaType == "text/html") && !utf8.Valid(data) {
		return page, errors.New("WEB_UNSUPPORTED_ENCODING")
	}
	var err error
	switch plan.Decoder {
	case "text":
		page.Text = string(data)
	case "html":
		page.Title, page.Text, err = ParseHTML(ctx, data)
	case "document":
		page.Title, page.Text, err = extractDocument(ctx, c, page.URL, page.MediaType, data)
	case "image":
		page.Image, page.Text, err = ParseImage(ctx, data, page.MediaType)
		return page, err
	default:
		return page, ErrRouteUnavailable
	}
	if err != nil {
		return page, err
	}
	if strings.TrimSpace(page.Text) == "" {
		return page, errors.New("WEB_EMPTY_OR_DYNAMIC_PAGE")
	}
	return page, nil
}
