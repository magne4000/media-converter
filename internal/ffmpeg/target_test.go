package ffmpeg

import (
	"slices"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		codec     string
		user      []Option
		wantCodec string
		wantOpts  []Option
		why       string
	}{{
		name: "a format brings its own encoder and quality", format: "ogg",
		wantCodec: "libvorbis", wantOpts: []Option{{Key: "q:a", Value: "7"}},
		why: "libvorbis on its own lands near 112 kbps",
	}, {
		name: "an alias resolves to the same target", format: "m4a",
		wantCodec: "aac", wantOpts: []Option{{Key: "b:a", Value: "256k"}},
		why: "FFmpeg's native AAC encoder is weaker than libfdk_aac",
	}, {
		name: "an encoder override drops the defaults", format: "ogg", codec: "libopus",
		wantCodec: "libopus",
		why:       "-q:a means nothing to libopus",
	}, {
		name: "user rate control replaces the default", format: "ogg",
		user: []Option{{Key: "b:a", Value: "192k"}}, wantCodec: "libvorbis",
		wantOpts: []Option{{Key: "b:a", Value: "192k"}},
		why:      "-q:a alongside -b:a leaves the outcome to the encoder",
	}, {
		name: "unrelated user options keep the defaults", format: "mp3",
		user: []Option{{Key: "ar", Value: "44100"}}, wantCodec: "libmp3lame",
		wantOpts: []Option{
			{Key: "q:a", Value: "0"}, {Key: "compression_level", Value: "0"}, {Key: "ar", Value: "44100"},
		},
		why: "analysis effort is orthogonal to rate control",
	}, {
		name: "lossless formats carry no rate control", format: "flac",
		wantCodec: "flac",
		why:       "the output is bit-exact whatever the setting",
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := Resolve(test.format, test.codec, test.user)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if target.Codec != test.wantCodec {
				t.Errorf("Codec = %q, want %q: %s", target.Codec, test.wantCodec, test.why)
			}
			if !slices.Equal(target.Options, test.wantOpts) {
				t.Errorf("Options = %v, want %v: %s", target.Options, test.wantOpts, test.why)
			}
		})
	}
}

func TestResolveRejectsAnUnknownFormat(t *testing.T) {
	_, err := Resolve("midi", "", nil)
	if err == nil || !strings.Contains(err.Error(), "ogg") {
		t.Errorf("Resolve(midi) = %v, want an error listing the formats", err)
	}
}

func TestEveryLossyFormatPinsItsRateControl(t *testing.T) {
	// FFmpeg's own defaults aim at streaming, so a lossy format that inherited
	// them would quietly encode a lossless source at ~128 kbps.
	for _, name := range Names() {
		format := formats[name]
		pinned := slices.ContainsFunc(format.Defaults, func(o Option) bool { return rateControl[o.Key] })

		if pinned == format.Lossless {
			t.Errorf("%s: lossless=%v but rate control pinned=%v", name, format.Lossless, pinned)
		}
		if format.Muxer == "" || format.Codec == "" || !strings.HasPrefix(format.Ext, ".") {
			t.Errorf("%s: incomplete entry %+v", name, format)
		}
	}
}

func TestArgs(t *testing.T) {
	target, err := Resolve("ogg", "", []Option{{Key: "shortest"}})
	if err != nil {
		t.Fatal(err)
	}

	got := strings.Join(target.Args("in.flac", "out.ogg"), " ")
	want := "-nostdin -hide_banner -loglevel error -y -i in.flac -map 0:a -map_metadata 0 " +
		"-c:a libvorbis -q:a 7 -shortest -f ogg out.ogg"
	if got != want {
		t.Errorf("Args =\n%s\nwant\n%s", got, want)
	}

	// Cover art follows the container: copied where one can be carried,
	// dropped where handing it to the muxer would abort the conversion.
	for name, wantArt := range map[string]bool{"mp3": true, "flac": true, "ogg": false, "opus": false} {
		target, err := Resolve(name, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(target.Args("in.flac", "out"), " ")
		if got := strings.Contains(args, "-map 0:v? -c:v copy"); got != wantArt {
			t.Errorf("%s: cover art mapped = %v, want %v", name, got, wantArt)
		}
	}
}

func TestQuoteLeavesPlainTokensBare(t *testing.T) {
	got := Quote([]string{"/usr/bin/ffmpeg", "-i", "/music/don't stop.flac", "-f", "ogg"})
	want := `/usr/bin/ffmpeg -i '/music/don'\''t stop.flac' -f ogg`
	if got != want {
		t.Errorf("Quote = %s, want %s", got, want)
	}
}
