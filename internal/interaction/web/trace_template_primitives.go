package web

import (
	"github.com/z-chenhao/assistant/internal/observability"
	"math"
	"time"
)

// Private scalar/time operations for the bounded Trace template. The resource
// chooses units, scale, range, maximum and labels; these operators hold no Trace
// policy or source access. They are not a public expression language or SDK.
func traceTemplateUnixMicro(raw string) int64 {
	value, _ := time.Parse(time.RFC3339Nano, raw)
	return value.UnixMicro()
}
func traceTemplateIntSub(a, b int64) (int64, error) {
	value := a - b
	if b > 0 && value > a || b < 0 && value < a {
		return 0, observability.ErrInvalid
	}
	return value, nil
}
func traceTemplateFloatSub(a, b float64) (float64, error) {
	value := a - b
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, observability.ErrInvalid
	}
	return value, nil
}
func traceTemplateFraction(numerator, denominator int64, scale float64) (float64, error) {
	if denominator == 0 {
		return 0, observability.ErrInvalid
	}
	value := float64(numerator) / float64(denominator) * scale
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, observability.ErrInvalid
	}
	return value, nil
}
