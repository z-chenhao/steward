package main

import (
	"context"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/codingworkspace"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, codingworkspace.Plugin()) != nil {
		os.Exit(1)
	}
}
