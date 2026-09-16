// Package ffmpeg owns everything about the FFmpeg process: where the
// executable is, what a container is called, which encoder and options produce
// a good file, and how a conversion command is built and run.
package ffmpeg

import (
	"cmp"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// EnvPath names the environment variable that pins the executable.
const EnvPath = "MEDIA_CONVERTER_FFMPEG"

// Locate resolves the executable to drive: the one named explicitly or in the
// environment, else ffmpeg on PATH. Bare names and paths both go through
// LookPath, which also rejects what cannot be executed.
func Locate(named string, getenv func(string) string) (string, error) {
	name := cmp.Or(named, getenv(EnvPath), ffmpegName())

	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w; %s", err, installHint())
	}
	return path, nil
}

func ffmpegName() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "install it with `brew install ffmpeg`, or pass --ffmpeg PATH"
	case "windows":
		return "install it with `winget install Gyan.FFmpeg`, or pass --ffmpeg PATH"
	default:
		return "install it with your package manager, for example `apt install ffmpeg`, or pass --ffmpeg PATH"
	}
}

// Run executes one conversion and returns FFmpeg's own diagnosis on failure.
func Run(ctx context.Context, path string, args []string) error {
	var stderr strings.Builder

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, lastLine(stderr.String()))
	}
	return nil
}

// lastLine is the line FFmpeg ended on, which is the one that says why.
func lastLine(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
