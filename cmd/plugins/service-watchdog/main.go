package main

import (
	"context"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/servicewatchdog"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, servicewatchdog.Plugin()) != nil {
		os.Exit(1)
	}
}
