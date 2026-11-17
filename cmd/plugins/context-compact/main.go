package main

import (
	"context"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/contextcompact"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, contextcompact.Plugin()) != nil {
		os.Exit(1)
	}
}
