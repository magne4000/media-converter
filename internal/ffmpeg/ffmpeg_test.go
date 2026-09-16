package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)

	named := filepath.Join(root, "ffmpeg")
	if err := os.WriteFile(named, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	unset := func(string) string { return "" }

	for _, test := range []struct {
		name   string
		given  string
		getenv func(string) string
		want   string
	}{
		{name: "named explicitly", given: named, getenv: unset, want: named},
		{name: "named in the environment", getenv: func(string) string { return named }, want: named},
		{name: "found on PATH", getenv: unset, want: named},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Locate(test.given, test.getenv)
			if err != nil || got != test.want {
				t.Errorf("Locate = %q, %v; want %q", got, err, test.want)
			}
		})
	}

	t.Setenv("PATH", filepath.Join(root, "nowhere"))
	_, err := Locate("", unset)
	if err == nil || !strings.Contains(err.Error(), "install it with") {
		t.Errorf("Locate without FFmpeg = %v, want an error that says how to install it", err)
	}
}

func TestRunReportsTheLineFFmpegEndedOn(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ffmpeg")
	body := "#!/bin/sh\necho noise >&2\necho 'Encoder not found' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	err := Run(context.Background(), script, nil)
	if err == nil || !strings.Contains(err.Error(), "Encoder not found") {
		t.Errorf("Run = %v, want FFmpeg's own last line", err)
	}
}
