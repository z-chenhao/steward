package main

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/taskprogress"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, taskprogress.Plugin()) != nil {
		os.Exit(1)
	}
}
