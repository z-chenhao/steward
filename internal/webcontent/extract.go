package webcontent

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"time"
)

type Config struct {
	ExtractCommand string
	ExtractScript  string
}

func (c Config) Validate() error {
	if (c.ExtractCommand == "") != (c.ExtractScript == "") || c.ExtractScript != "" && (!filepath.IsAbs(c.ExtractCommand) || !filepath.IsAbs(c.ExtractScript)) {
		return errors.New("web extractor requires absolute command and script")
	}
	return nil
}

func extractDocument(ctx context.Context, c Config, source, kind string, data []byte) (string, string, error) {
	if decoder, ok := ctx.Value(documentParserKey{}).(documentDecoder); ok {
		return decoder.parse(ctx, source, kind, data)
	}
	return extractDocumentLegacyV1(ctx, c, source, kind, data)
}
func FetchContent(ctx context.Context, c Config, raw, mode string) (Page, error) {
	return fetchContent(ctx, c, raw, mode, publicClient())
}
func fetchContent(ctx context.Context, c Config, raw, mode string, client interface {
	Do(*http.Request) (*http.Response, error)
}) (Page, error) {
	page := Page{URL: raw, Untrusted: true, Freshness: "fetched_now; origin may serve cached content"}
	if ValidURL(raw) != nil {
		return page, errBlocked
	}
	if mode != "readable" && mode != "raw" && mode != "answer" && mode != "" {
		return page, errors.New("INVALID_ARGUMENT")
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return page, errBlocked
	}
	request.Header.Set("User-Agent", "Steward-WebAccess/2.0")
	request.Header.Set("Accept", "text/html,text/markdown,text/plain,application/pdf,image/png,image/jpeg")
	response, e := client.Do(request)
	if e != nil {
		if ctx.Err() != nil {
			return page, ctx.Err()
		}
		return page, errors.New("WEB_FETCH_FAILED")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return page, errors.New("WEB_HTTP_OR_ACCESS_ERROR")
	}
	kind, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil {
		return page, errors.New("WEB_UNSUPPORTED_CONTENT")
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if e != nil || len(data) > 8<<20 {
		return page, errors.New("RESOURCE_LIMIT")
	}
	page.URL = response.Request.URL.String()
	page.MediaType = kind
	page.RetrievedAt = time.Now().UTC().Format(time.RFC3339)
	return DecodePage(ctx, c, page, "content", mode, data)
}
