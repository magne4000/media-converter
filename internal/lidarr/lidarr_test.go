package lidarr

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestRead(t *testing.T) {
	environment := map[string]string{
		EnvEventType: " Download ",
		// Some Lidarr versions leave an empty segment behind.
		EnvTrackPaths: " /music/a.flac | |/music/b.flac",
	}
	event := Read(func(key string) string { return environment[key] })

	if !event.Active() || event.Type != "Download" {
		t.Errorf("event = %+v, want an active Download", event)
	}
	want := []string{filepath.Clean("/music/a.flac"), filepath.Clean("/music/b.flac")}
	if !slices.Equal(event.TrackPaths, want) {
		t.Errorf("TrackPaths = %v, want %v", event.TrackPaths, want)
	}
}

func TestEmptyEnvironmentIsNotLidarr(t *testing.T) {
	event := Read(func(string) string { return "  " })

	if event.Active() || event.TrackPaths != nil {
		t.Errorf("event = %+v, want nothing", event)
	}
}

func TestIsTestIgnoresCase(t *testing.T) {
	// The Test button's event must be recognised whatever its casing, or the
	// connection test reports failure.
	for _, value := range []string{"Test", "test", "TEST"} {
		if !(Event{Type: value}).IsTest() {
			t.Errorf("IsTest(%q) = false", value)
		}
	}
	if (Event{Type: "Download"}).IsTest() {
		t.Error("IsTest(Download) = true")
	}
}
