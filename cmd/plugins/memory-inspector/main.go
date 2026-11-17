package main

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/memoryinspector"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, memoryinspector.Handlers()) != nil {
		os.Exit(1)
	}
}
