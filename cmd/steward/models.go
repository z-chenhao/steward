package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/z-chenhao/assistant/internal/app"
	"github.com/z-chenhao/assistant/internal/llm"
)

func runModel(ctx context.Context, application *app.App, flags *flag.FlagSet, in io.Reader, out io.Writer, input llm.ProfileInput, fixture bool, errOut io.Writer) error {
	switch flags.Arg(1) {
	case "accounts":
		accounts, err := application.Models().Accounts(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(accounts)
	case "login-chatgpt":
		connection, err := application.Accounts().Login(ctx, flags.Arg(2), func(event llm.AuthEvent) { _ = json.NewEncoder(out).Encode(event) })
		if err != nil {
			credentialHelp(errOut, err)
			return err
		}
		return json.NewEncoder(out).Encode(connection)
	case "refresh":
		connection, _, err := application.Accounts().Ensure(ctx, flags.Arg(2))
		if err != nil {
			credentialHelp(errOut, err)
			return err
		}
		return json.NewEncoder(out).Encode(connection)
	case "logout":
		revoked, err := application.Accounts().Logout(ctx, flags.Arg(2))
		if err != nil {
			credentialHelp(errOut, err)
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"status": "logged_out", "remote_revocation_confirmed": revoked})
	case "account-catalog", "account-add":
		auth, _, err := application.Accounts().Ensure(ctx, flags.Arg(2))
		if err != nil {
			return err
		}
		models, err := application.AccountCatalog(ctx, auth.ConnectionID)
		if err != nil {
			return err
		}
		if flags.Arg(1) == "account-catalog" {
			return json.NewEncoder(out).Encode(models)
		}
		for _, m := range models {
			if m.ModelID == flags.Arg(3) {
				input := llm.ProfileInput{Name: m.ModelID, ProviderKind: "openai_account", WireAPI: "openai_responses", ModelID: m.ModelID, AuthConnectionID: auth.ConnectionID, ContextWindow: m.ContextWindow, MaxOutputTokens: min(m.MaxOutputTokens, m.ContextWindow), Compatibility: llm.Compatibility{ToolResultFormat: "openai"}, Capabilities: llm.Capabilities{TextInput: true, ImageInput: m.ImageInput}}
				profile, err := application.Models().AddProfile(ctx, input)
				if err != nil {
					return err
				}
				return json.NewEncoder(out).Encode(profile)
			}
		}
		return llm.ModelNotFound
	case "discover":
		models, err := application.Discover(ctx, flags.Arg(2), flags.Arg(3))
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(models)
	case "login":
		var connection llm.AuthConnection
		var err error
		if flags.NArg() == 3 {
			connection, err = application.LoginEnvironment(ctx, flags.Arg(2))
		} else {
			value, readErr := io.ReadAll(io.LimitReader(in, 8193))
			if readErr != nil || len(value) > 8192 {
				return llm.ErrSecretStore
			}
			value = []byte(strings.TrimSuffix(strings.TrimSuffix(string(value), "\n"), "\r"))
			connection, err = application.LoginKey(ctx, value)
			for i := range value {
				value[i] = 0
			}
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(connection)
	case "catalog":
		var models []llm.CatalogModel
		var err error
		if flags.NArg() == 3 {
			models, err = application.CatalogProfile(ctx, flags.Arg(2))
		} else {
			models, err = application.Catalog(ctx)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(models)
	case "test":
		r, err := application.ModelControl().Test(ctx, flags.Arg(2))
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	case "select":
		if !fixture {
			fmt.Fprintln(errOut, "Privacy: model requests and tool schemas go only to the selected provider (OpenAI account requests use https://api.openai.com/v1) or your configured local endpoint. localhost resolves in the Go Host network namespace, not the Computer Guest. Credentials stay in SecretStore and transient Bridge memory. Steward keeps the selected generation provider. Web Access sends public queries and requested evidence only to its frozen configured search sources.")
		}
		pv, err := strconv.ParseInt(flags.Arg(3), 10, 64)
		if err != nil || pv < 1 {
			return llm.ErrConflict
		}
		cv, err := strconv.ParseInt(flags.Arg(4), 10, 64)
		if err != nil || cv < 1 {
			return llm.ErrConflict
		}
		if err := application.ModelControl().Select(ctx, flags.Arg(2), pv, cv); err != nil {
			return err
		}
		d, err := application.Diagnostics(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(d)
	case "cancel":
		d, err := application.Diagnostics(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(d)
	}
	if flags.Arg(1) == "list" {
		profiles, err := application.Models().ListProfiles(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(profiles)
	}
	if flags.Arg(1) == "add" && flags.NArg() >= 4 {
		var err error
		input, err = application.PrepareModelShortcut(ctx, input)
		if err != nil {
			return err
		}
	}
	profile, err := application.Models().AddProfile(ctx, input)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(profile)
}

func credentialHelp(out io.Writer, err error) {
	if errors.Is(err, llm.ErrSecretStore) || errors.Is(err, llm.ErrSecretCleanup) {
		fmt.Fprintln(out, "Check your private credential files and their permissions, then retry. Run steward doctor; see docs/features/model-connections.md.")
	}
}
