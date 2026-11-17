package main

import (
	"context"
	"github.com/z-chenhao/assistant/internal/plugin/githubfollowup"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
	"os"
)

func main() {
	if rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, githubfollowup.Plugin()) != nil {
		os.Exit(1)
	}
}
