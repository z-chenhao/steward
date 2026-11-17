package main

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/taskfeedback"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, taskfeedback.Plugin()) != nil {
		os.Exit(1)
	}
}
