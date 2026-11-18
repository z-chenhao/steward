package main

import (
	"context"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/taskmail"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if err := rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, taskmail.Plugin()); err != nil {
		os.Exit(1)
	}
}
