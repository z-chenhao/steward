package main

import (
	"context"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/referencepricing"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, referencepricing.Plugin()) != nil {
		os.Exit(1)
	}
}
