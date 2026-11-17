package main

import (
	"context"
	"fmt"
	"os"

	"github.com/z-chenhao/assistant/internal/plugin/appsinterface"
	"github.com/z-chenhao/assistant/pkg/rsiplugin"
)

func main() {
	if err := rsiplugin.Serve(context.Background(), os.Stdin, os.Stdout, appsinterface.Handlers()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
