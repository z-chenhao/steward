package main

import (
	"context"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/shellpolicy"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, shellpolicy.Plugin()) != nil {
		os.Exit(1)
	}
}
