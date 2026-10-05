# Subsplash Generator

Marks, trims and stitches livestream service recordings into videos ready for Subsplash.

## Development

Requires Go and, for tests, ffmpeg and ffprobe on PATH (or `SG_FFMPEG_DIR` set to the folder holding them).

```sh
go test ./...
go run ./cmd/sg trim -start 00:31:04 -end 01:12:40 recording.mkv trimmed.mp4
go run ./cmd/sg stitch -intro intro.png -outro outro.mp4 trimmed.mp4 final.mp4
```

The app only runs the ffmpeg bundled beside it, in an `ffmpeg` folder next to the executable. `SG_FFMPEG_DIR` overrides that folder for development.
