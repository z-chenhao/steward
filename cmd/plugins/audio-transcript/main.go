package main

import (
	"context"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/audiotranscript"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if err := rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, audiotranscript.Handlers()); err != nil {
		os.Exit(1)
	}
}
