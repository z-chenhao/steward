package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"

	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/codingdraft"
)

func runCodingDraft(ctx context.Context, a *app.App, flags *flag.FlagSet, in io.Reader, out io.Writer) error {
	var target codingdraft.Target
	if flags.Arg(1) == "draft-plan" {
		raw, err := io.ReadAll(io.LimitReader(in, 8193))
		if err != nil || len(raw) > 8192 {
			return codingdraft.ErrInvalid
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&target) != nil || d.Decode(&struct{}{}) != io.EOF || !target.Valid() {
			return codingdraft.ErrInvalid
		}
	}
	receipt, err := a.CodingDraft(ctx, flags.Arg(1), flags.Arg(2), flags.Arg(3), target)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(receipt)
}
