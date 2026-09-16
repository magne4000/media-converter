# media-converter

A single Go binary that batch-converts audio files by driving FFmpeg. It runs
conversions concurrently, deletes a source only once its replacement is on
disk, and doubles as a Lidarr custom script.

```sh
media-converter -i /music -o /converted -f ogg -c 8
```

## Install

Go 1.25 or newer is the only prerequisite.

```sh
make build      # host binary
make all        # dist/ binaries for linux, darwin and windows on amd64 and arm64
make install    # install into GOBIN
```

## FFmpeg

FFmpeg is required. It is taken from `--ffmpeg PATH` or
`MEDIA_CONVERTER_FFMPEG`, else from `ffmpeg` on `PATH`; when there is none, the
error says how to install one.

## Usage

```
media-converter [options] [PATH...]
media-converter [options] -i PATH [PATH...]
```

| Option | Effect |
|--------|--------|
| `-i, --input PATH...` | Files, directories (recursive) or globs (quote them; expanded internally). In Lidarr mode these filter `lidarr_addedtrackpaths` instead. |
| `--all-formats` | Accept lossy inputs too, not just lossless. |
| `-o, --output DIR` | Destination directory. Default: working directory; Lidarr mode: beside each input. |
| `-f, --format FORMAT` | Output container, default `ogg`; `--help` lists them. Each brings an encoder and a quality suited to archiving a lossless source, overridable with `--acodec=`, `--q:a=` or `--b:a=`. |
| `-r, --remove` | Delete each source after its own conversion succeeds. |
| `-c, --concurrency N` | Simultaneous conversions. Default `4`. |
| `-n, --dry-run` | Print the exact ffmpeg command line per file and exit without writing or removing anything. |
| `--KEY=VALUE` | Forwarded to ffmpeg as `-KEY VALUE`, in order. |
| `--ffmpeg PATH` | Use this executable (or set `MEDIA_CONVERTER_FFMPEG`). |
| `-h, --help` / `--version` | Usage, version. |

Short switches cluster and take attached values, so `-rn` is `-r -n` and `-c8`
is `-c 8`. Repeating any option is an error; only `-i` may repeat.

### Input

Positional arguments are inputs too. Globbing is done by the program, so `**`
works in every shell. An argument that matches no accepted audio file is
reported and the others are still processed.

Accepted by default (lossless only): `.flac .wav .wave .w64 .aif .aiff .aifc
.alac .ape .wv .tta .shn .dsf .dff`. `--all-formats` additionally accepts
`.mp3 .m4a .m4b .mp4 .aac .adts .ogg .oga .opus .spx .wma .mp2 .mpc .ac3 .eac3
.dts .amr .au .caf .mka .oma .ra` — `.m4a` is lossy far more often than ALAC.

### Output

The base name is preserved and the extension replaced, so `/music/song.flac
-f ogg` becomes `song.ogg`. Each output is written to a temporary file in the
destination and renamed into place only after FFmpeg exits successfully, with
the source's permissions, so an interrupted run leaves no truncated tracks and
no unreadable ones.

Refused before any work starts: an output that would overwrite its own input,
two inputs that would produce the same output, and an output that is itself one
of the inputs. Paths differing only in case count as the same file, because on
macOS and Windows they are. `-r` deletes a source only after that rename.

Failures are per file: each error names its input on stderr, the rest continue,
and the exit status is non-zero. Cover art is copied where the container can
carry an attached picture and dropped where it cannot, since feeding one to
such a muxer aborts the conversion.

### FFmpeg options

* Unknown `--KEY=VALUE` options are forwarded as `-KEY VALUE`, in order.
* `--acodec`, `--c:a` and `--codec:a` select the encoder.
* Keys may contain `:`, so only the first `=` delimits: `--q:a=7` is `q:a` `7`.
* `--KEY=` with an empty value forwards a valueless flag: `--shortest=` becomes
  `-shortest`.

## Lidarr

1. Put a wrapper script next to the binary, because Lidarr's Custom Script
   connection has no arguments field and runs the executable bare:

```bash
#!/usr/bin/env bash
set -euo pipefail

here="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

exec "$here/media-converter" -f ogg -c 4 -i '*.flac' '*.wav'
```

2. `chmod +x` it, and make sure FFmpeg is on the `PATH` Lidarr runs with — a
   service process often has a minimal one, so `--ffmpeg /usr/bin/ffmpeg` is
   the reliable form.
3. `Settings → Connect → + → Custom Script`, **Path**: that script.
4. **Notification Triggers**: enable *On Release Import* (and *On Upgrade*).

Two variables are read: `lidarr_eventtype` selects Lidarr mode, and `Test`
exits 0 at once so Lidarr's Test button succeeds; `lidarr_addedtrackpaths` is
the `|`-separated list of imported files.

Progress and the run summary go to stdout, which Lidarr logs at debug level;
only failures go to stderr, which Lidarr logs at `Error` level whatever it
contains. So an `Error` entry from this tool is always a real one.

In Lidarr mode `-i` is a *filter* over the imported tracks rather than a source
(no separator matches the base name, a separator the whole path), `-o` is
optional and defaults to beside each input, and the lossless-only default and
`-r` still apply.

## Examples

```sh
media-converter song.flac                                  # into the working directory
media-converter -o /converted -f ogg -c 8 -i /music        # -q:a 7 applied for you
media-converter -i '/music/**/*.flac'                      # recursive glob, quoted
media-converter -n -f opus -i /music                       # print commands, convert nothing
media-converter -i /music --all-formats -r                 # lossy too, delete each original
media-converter --ffmpeg /opt/homebrew/opt/ffmpeg@7/bin/ffmpeg -f ogg -i /music
```

## Licence

MIT (see [LICENSE](LICENSE)). The FFmpeg you point it at keeps its own licence.
AAC and HEVC carry patent considerations in some jurisdictions; Vorbis, Opus and
FLAC are royalty-free.
