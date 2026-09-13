package main

import (
	"context"
	"github.com/ni00/wave-ai/cli/internal/command"
	"os"
	"os/signal"
	"syscall"
)

var version = "0.1.0"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := command.Execute(ctx, version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
