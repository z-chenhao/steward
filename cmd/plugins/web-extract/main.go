package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/z-chenhao/assistant/internal/plugin/webextract"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve() error {
	entry, err := os.Executable()
	if err != nil {
		return err
	}
	// The Host verifies this sealed package before launching it. Reading only
	// its own manifest lets the unchanged wrapper serve private owner variants;
	// no environment or invocation input can override the package identity.
	f, err := os.Open(filepath.Join(filepath.Dir(entry), "steward.plugin.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > rsiplugin.MaxFrameBytes {
		return fmt.Errorf("invalid package manifest")
	}
	raw, err := io.ReadAll(io.LimitReader(f, rsiplugin.MaxFrameBytes+1))
	if err != nil {
		return err
	}
	var manifest rsiplugin.Manifest
	if err := rsiplugin.Decode(raw, &manifest); err != nil {
		return err
	}
	if manifest.Protocol != rsiplugin.Protocol || manifest.PluginID != "steward.web-extract" || manifest.Version == "" {
		return fmt.Errorf("invalid package identity")
	}
	return rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, webextract.Handlers(manifest.Version))
}
