package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/z-chenhao/assistant/internal/llm"
)

func readModelInput(command command, flags *flag.FlagSet, in io.Reader) (llm.ProfileInput, error) {
	var input llm.ProfileInput
	if command.kind == commandModel && flags.Arg(1) == "add" && flags.NArg() >= 4 {
		input = llm.ProfileInput{Name: flags.Arg(3), ProviderKind: "openai_compatible", Endpoint: flags.Arg(2), ModelID: flags.Arg(3)}
		if flags.NArg() == 5 {
			input.AuthConnectionID = flags.Arg(4)
		}
		if err := input.ValidateIdentity(); err != nil {
			return input, err
		}
	}
	if command.kind == commandModel && flags.Arg(1) == "add" && flags.NArg() == 2 {
		data, err := io.ReadAll(io.LimitReader(in, 65537))
		if err != nil || len(data) > 65536 {
			return input, errors.New("expected one model metadata object of at most 64 KiB")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return input, errors.New("invalid model metadata JSON (unknown fields, types, or syntax)")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return input, errors.New("expected one model metadata object of at most 64 KiB")
		}
		if err := input.Validate(); err != nil {
			return input, err
		}
	}
	return input, nil
}
