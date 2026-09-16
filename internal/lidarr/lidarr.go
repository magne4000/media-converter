// Package lidarr reads the environment Lidarr gives a custom script
package lidarr

import (
	"path/filepath"
	"strings"
)

const (
	// EnvEventType selects Lidarr mode; EnvTrackPaths supplies the sources.
	EnvEventType  = "lidarr_eventtype"
	EnvTrackPaths = "lidarr_addedtrackpaths"

	// PathSeparator delimits Lidarr's path lists.
	PathSeparator = "|"

	// EventTest is what the Test button sends: no tracks, and succeeding is
	// what makes the connection show as valid.
	EventTest = "Test"
)

// Event is one Lidarr invocation.
type Event struct {
	Type       string
	TrackPaths []string
}

func Read(getenv func(string) string) Event {
	return Event{
		Type:       strings.TrimSpace(getenv(EnvEventType)),
		TrackPaths: parsePaths(getenv(EnvTrackPaths)),
	}
}

// Active reports whether Lidarr started this process.
func (e Event) Active() bool { return e.Type != "" }

func (e Event) IsTest() bool { return strings.EqualFold(e.Type, EventTest) }

// parsePaths splits a Lidarr path list, dropping the empty segments some
// versions leave behind on single-entry lists.
func parsePaths(list string) []string {
	var paths []string

	for _, part := range strings.Split(list, PathSeparator) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			paths = append(paths, filepath.Clean(trimmed))
		}
	}
	return paths
}
