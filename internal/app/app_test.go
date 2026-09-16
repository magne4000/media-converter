package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"media-converter/internal/cli"
	"media-converter/internal/ffmpeg"
	"media-converter/internal/lidarr"
)

// payload is what the fake FFmpeg writes, so a test can tell an output this run
// produced from a file that was already there.
const payload = "FAKEMEDIA"

// harness runs the program with a controlled environment and a fake FFmpeg, so
// that mode selection, stream routing and exit status are tested without
// encoding anything.
type harness struct {
	t      *testing.T
	root   string
	ffmpeg string
	env    map[string]string
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T, failOn string) *harness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake FFmpeg is a shell script")
	}

	root := t.TempDir()
	// Nothing may leak in from the machine running the tests.
	t.Setenv("PATH", filepath.Join(root, "nowhere"))
	t.Setenv(ffmpeg.EnvPath, "")

	h := &harness{t: t, root: root, env: map[string]string{}}
	script := "#!/bin/sh\n"
	if failOn != "" {
		script += fmt.Sprintf("case \"$*\" in *%s*) echo 'Invalid data' >&2; exit 1 ;; esac\n", failOn)
	}
	script += "for last in \"$@\"; do :; done\nprintf '%s' '" + payload + "' > \"$last\"\n"

	h.ffmpeg = filepath.Join(root, "bin", "ffmpeg")
	if err := os.MkdirAll(filepath.Dir(h.ffmpeg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) run(args ...string) error {
	h.t.Helper()

	h.stdout.Reset()
	h.stderr.Reset()
	return Run(context.Background(), Env{
		Args:    append([]string{"--ffmpeg", h.ffmpeg}, args...),
		Getenv:  func(key string) string { return h.env[key] },
		Stdout:  &h.stdout,
		Stderr:  &h.stderr,
		Version: "test",
	})
}

// source writes a file under the harness's music directory.
func (h *harness) source(name string) string {
	h.t.Helper()

	path := filepath.Join(h.root, "music", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func (h *harness) music() string { return filepath.Join(h.root, "music") }
func (h *harness) out() string   { return h.stdout.String() }
func (h *harness) err() string   { return h.stderr.String() }

func TestRunSuccessWritesNothingToStderr(t *testing.T) {
	// Lidarr logs a custom script's stderr at Error level whatever it says, so
	// anything there on a good run shows up as a failure in its UI.
	h := newHarness(t, "")
	h.source("a.flac")
	h.source("b.wav")
	out := filepath.Join(h.root, "out")

	if err := h.run("-f", "flac", "-o", out, "-i", h.music()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.err() != "" {
		t.Errorf("stderr = %q, want empty", h.err())
	}
	if !strings.Contains(h.out(), "2 converted, 0 failed") {
		t.Errorf("stdout = %q, want the summary", h.out())
	}
	for _, name := range []string{"a.flac", "b.flac"} {
		if data, err := os.ReadFile(filepath.Join(out, name)); err != nil || string(data) != payload {
			t.Errorf("%s = %q, %v; want the converted payload", name, data, err)
		}
	}
}

func TestRunReportsProblemsAndKeepsGoing(t *testing.T) {
	// One broken input, one missing path, one good file: the good one converts,
	// the other two are named on stderr and counted against the exit status.
	h := newHarness(t, "broken")
	good := h.source("good.flac")
	broken := h.source("broken.flac")
	out := filepath.Join(h.root, "out")

	err := h.run("-f", "flac", "-o", out, "-i", good, broken, filepath.Join(h.root, "gone.flac"))
	if err == nil {
		t.Fatal("Run returned nil with a failed and an unusable input")
	}
	if !strings.Contains(h.out(), "1 converted, 1 failed, 1 refused") {
		t.Errorf("stdout = %q, want all three outcomes counted", h.out())
	}
	for _, want := range []string{"broken.flac", "gone.flac"} {
		if !strings.Contains(h.err(), want) {
			t.Errorf("stderr = %q, want it to name %s", h.err(), want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(out, "good.flac")); statErr != nil {
		t.Errorf("the usable input was not converted: %v", statErr)
	}
}

func TestRunLidarrMode(t *testing.T) {
	// Sources come from the environment, -i filters them, output lands beside
	// each input, and a lossy track stays out by default.
	h := newHarness(t, "")
	track := h.source("album track.flac")
	lossy := h.source("bonus.mp3")
	h.env[lidarr.EnvEventType] = "Download"
	h.env[lidarr.EnvTrackPaths] = strings.Join([]string{track, lossy}, lidarr.PathSeparator)

	if err := h.run("-f", "ogg", "-i", "*.flac"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.err() != "" {
		t.Errorf("stderr = %q, want empty", h.err())
	}
	if data, err := os.ReadFile(filepath.Join(h.music(), "album track.ogg")); err != nil || string(data) != payload {
		t.Errorf("output is not beside its input: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(h.music(), "bonus.ogg")); !os.IsNotExist(err) {
		t.Error("the lossy track was converted despite the lossless-only default")
	}
}

func TestRunLidarrNoOpEvents(t *testing.T) {
	// The Test button sends no tracks, and an album of MP3s imported by a
	// FLAC-only script is not an error. Both must exit 0 and stay quiet.
	for _, tc := range []struct {
		name  string
		event string
		paths func(*harness) string
		want  string
	}{
		{name: "test button", event: lidarr.EventTest, paths: func(*harness) string { return "" }, want: "connection test OK"},
		{name: "nothing matches", event: "Download", paths: func(h *harness) string { return h.source("bonus.mp3") }, want: "no matching tracks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "")
			h.env[lidarr.EnvEventType] = tc.event
			h.env[lidarr.EnvTrackPaths] = tc.paths(h)

			if err := h.run("-f", "flac"); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(h.out(), tc.want) || h.err() != "" {
				t.Errorf("stdout = %q, stderr = %q, want %q and silence", h.out(), h.err(), tc.want)
			}
		})
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	h := newHarness(t, "")
	src := h.source("a.flac")
	out := filepath.Join(h.root, "out")

	if err := h.run("-n", "-r", "-f", "flac", "-o", out, "-i", src); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"-f flac", "then remove " + src, "1 conversion planned"} {
		if !strings.Contains(h.out(), want) {
			t.Errorf("stdout = %q, want %q", h.out(), want)
		}
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("dry run created the output directory")
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("dry run removed the source: %v", err)
	}
}

func TestRunUsageAndErrors(t *testing.T) {
	h := newHarness(t, "")

	// --help must work even alongside a format that does not exist, so that a
	// user who mistyped -f can discover the valid values.
	if err := h.run("-f", "midi", "--help"); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(h.out(), "USAGE") || !strings.Contains(h.out(), "--dry-run") {
		t.Errorf("stdout = %q, want the usage text", h.out())
	}

	if err := h.run("--version"); err != nil || !strings.Contains(h.out(), "test") {
		t.Errorf("--version = %v, %q", err, h.out())
	}
	if err := h.run("-f", "midi", "-i", h.source("a.flac")); err == nil {
		t.Error("an unsupported format was accepted")
	}
	if err := h.run("-f", "flac"); !errors.Is(err, cli.ErrNoInput) {
		t.Errorf("no inputs = %v, want cli.ErrNoInput", err)
	}
}
