package cli

import (
	"fmt"
	"strings"

	"media-converter/internal/ffmpeg"
)

var sections = []string{"INPUT", "OUTPUT", "FFMPEG", "OTHER"}

// Usage is rendered from the flag table, so an option cannot exist in the
// parser and be missing here.
func Usage(version string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "media-converter %s - convert audio files with FFmpeg\n\n", version)
	b.WriteString("USAGE\n  media-converter [options] [PATH...]\n")

	for _, section := range sections {
		fmt.Fprintf(&b, "\n%s\n", section)
		for _, f := range flags {
			if f.section == section {
				fmt.Fprintf(&b, "  %-22s %s\n", spelling(f), f.help)
			}
		}
		if section == "FFMPEG" {
			fmt.Fprintf(&b, "  %-22s %s\n", "--KEY=VALUE", "forwarded to FFmpeg as -KEY VALUE, in order")
		}
	}

	fmt.Fprintf(&b, `
FORMAT is one of
  %s
each with an encoder and quality suited to archiving, overridable with
--acodec=, --q:a= or --b:a=. --ffmpeg is also read from %s.

EXAMPLES
  media-converter song.flac
  media-converter -i '/music/**/*.flac' -o /converted -c 8
  media-converter -f mp3 --b:a=320k -i /music
  media-converter -n -f opus -i /music
`, strings.Join(ffmpeg.Names(), ", "), ffmpeg.EnvPath)

	return b.String()
}

func spelling(f flag) string {
	spelt := "    " + f.long
	if f.short != "" {
		spelt = f.short + ", " + f.long
	}
	if f.metavar != "" {
		spelt += " " + f.metavar
	}
	return spelt
}
