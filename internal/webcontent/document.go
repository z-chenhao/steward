package webcontent

import "context"

type documentParserKey struct{}
type DocumentParser func(context.Context, string, string, []byte) (string, string, error)
type documentDecoder struct {
	configured bool
	parse      DocumentParser
}

// WithDocumentParser binds the original authorized page decoder, without URL
// fetching, credentials, paths or arbitrary process selection.
func WithDocumentParser(ctx context.Context, configured bool, parse DocumentParser) context.Context {
	return context.WithValue(ctx, documentParserKey{}, documentDecoder{configured: configured, parse: parse})
}

func ParseDocument(ctx context.Context, c Config, url, kind string, data []byte) (string, string, error) {
	return extractDocument(ctx, c, url, kind, data)
}
