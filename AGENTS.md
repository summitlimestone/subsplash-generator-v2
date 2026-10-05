# Working rules for AI agents in this repo

- Never commit directly to `main`. Always work on a separate branch,
  create one first if one isn't already checked out.
- Commit as much and as often as makes sense on that branch. No need to
  hold back or batch changes into one big commit.
- Keep commit messages extremely brief.
- Push each commit to `origin` right after making it, on whatever branch
  is checked out.
- Never merge a branch into `main`, or otherwise update or push `main`
  on the remote. Only the repo owner merges and pushes `main`.

# Project notes

- Design doc: https://claude.ai/code/artifact/c7546fe1-f499-4384-9560-c54673c82545
- ffmpeg/ffprobe are bundled with the app and are the only copies it runs.
  Never add a PATH lookup. `SG_FFMPEG_DIR` overrides the folder for
  development and tests only.
- Errors are returned, never handled by exiting the process.
- Keep comments short: say why, not the history of how it got here.
- Tests run real ffmpeg against small generated clips: `go test ./...`
  (needs ffmpeg/ffprobe on PATH, or `SG_FFMPEG_DIR` set).
