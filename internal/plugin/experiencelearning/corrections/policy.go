// Package corrections constructs a private method from explicitly authorized
// procedural steps. Host owns the independent user evidence and promotion.
package corrections

import (
	"context"
	"errors"
	"fmt"
	"github.com/z-chenhao/assistant/internal/plugin/experiencelearning/contract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"strings"
)

func Call(ctx context.Context, c rsiplugin.Call) (rsiplugin.Result, error) {
	var q contract.CorrectionInput
	if err := rsiplugin.Decode(c.Input, &q); err != nil {
		return rsiplugin.Result{}, err
	}
	if len(q.Steps) < 2 || len(q.Steps) > 16 {
		return rsiplugin.Result{}, errors.New("invalid method steps")
	}
	lines := make([]string, 0, len(q.Steps))
	for i, s := range q.Steps {
		step := strings.TrimSpace(s)
		if step == "" || strings.ContainsAny(step, "\r\n") || len(step) > 512 {
			return rsiplugin.Result{}, errors.New("invalid method step")
		}
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, step))
	}
	return rsiplugin.JSONResult(ctx, contract.CorrectionPlan{IndependentRuns: 2, Procedure: strings.Join(lines, "\n")})
}
