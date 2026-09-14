package main

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"os"
	"os/signal"
	"syscall"
	"wave-ai.local/wave/internal/platform/observe"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	if err := observe.Configure(os.Stderr, os.Getenv("WAVE_LOG_LEVEL")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := runCommand(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wave:", err)
		os.Exit(1)
	}
}
