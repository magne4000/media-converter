// Command media-converter converts audio files with FFmpeg.
//
// It runs both as an interactive CLI and as a Lidarr custom script; the mode is
// chosen by the presence of Lidarr's environment variables. Everything except
// process wiring lives in internal/app, so that it can be tested without a
// process.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"media-converter/internal/app"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := app.Run(ctx, app.Env{
		Args:    os.Args[1:],
		Getenv:  os.Getenv,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
}
