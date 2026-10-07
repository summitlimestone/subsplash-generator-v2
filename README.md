# Subsplash Generator

Turns a recorded church service into a sermon video ready for Subsplash. It
marks where the sermon starts and ends while OBS records, trims the
recording down to the sermon, and adds your series' intro and outro.

## Install

1. Download **SubsplashGenerator-Setup.exe** from the
   [latest release](https://github.com/summitlimestone/subsplash-generator-v2/releases/latest).
2. Run it. If Windows says "Windows protected your PC", click **More info**,
   then **Run anyway**. (The installer isn't signed, so Windows doesn't
   recognize it yet.)
3. Click **Next**. It installs for your Windows user only, so it doesn't ask
   for an administrator password, and adds Start menu and desktop shortcuts.

It runs on 64-bit Windows 10 and 11. Everything it needs is included.

## First run

A welcome screen walks you through two things:

- **Bringing over the old version's settings and series**, if the old
  version was used on this computer.
- **Connecting to OBS.** In OBS, open **Tools**, **WebSocket Server
  Settings**, tick **Enable WebSocket server** and click **Show Connect
  Info**. Copy the port and password into the welcome screen and click
  **Connect**.

You can change all of this later on the **Settings** tab.

## Setting up

### Series

Each video gets the intro and outro of its series. On the **Series** tab,
click **New series**, choose an intro and outro (a video, or a still image
shown for a few seconds) and a transition, and click **Save**. Hidden series
stay out of the lists without being deleted.

### ProPresenter (optional)

The app can mark the sermon by itself when particular slides come up, such
as the sermon title and the closing prayer. It works with older versions of
ProPresenter through their Stage Display connection.

1. In ProPresenter's network preferences, turn on the network and the Stage
   App (or Stage Display), and set its password.
2. On the app's **Settings** tab, enter the ProPresenter computer's name or
   IP address (`localhost` if it's the same computer), the port and the
   Stage App password, and click **Save**.
3. Show the slide that starts the sermon in ProPresenter. It appears under
   **Recent slides**, where you can pick it as the **Sermon start slide**.
   Do the same for the end. Or match any slide by its text instead.

Without ProPresenter, mark the sermon with the buttons.

### The OBS dock

The Live controls can sit inside OBS as a dock:

1. On the **Settings** tab, under **OBS dock**, click **Copy**.
2. In OBS, open **Docks**, **Custom Browser Docks**, give it a name (such
   as "Service Video"), paste the address as the URL and click **Apply**.

To use the dock from another computer, tick **Allow other computers on the
network**, save, and restart the app.

## Recording a service

On the **Live** tab (or the OBS dock):

1. Choose the series and click **Start watching**.
2. Start recording in OBS as usual.
3. The sermon's start and end are marked when the chosen slides come up,
   or when you click **Mark start** and **Mark end**. Clicking a Mark
   button again moves that mark to now.
4. Stop recording in OBS.
5. To fine-tune the marks, click **Edit marks** to open the recording in
   the editor.
6. Click **Trim**. When it's done, **Check trim** opens the trimmed video in
   the editor if you want to look it over.
7. Click **Stitch** to add the intro and outro.

The finished video is saved in `Videos\Subsplash`, named by date (such as
`2026-10-05.mp4`). If OBS or ProPresenter drops out during the service, the
app reconnects by itself and keeps the marks it has.

## Older recordings: Bulk edit

For a backlog of recordings that were never cut:

1. On the **Bulk edit** tab, click **Choose folder** and pick the folder
   holding them. Subfolders are included.
2. Click **Mark next**. Find the sermon's start and click **Set start**,
   find its end and click **Set end**, check the date and series, and click
   **Save & next**. Use **Skip** for recordings without a sermon.
3. When they're marked, render them on the **Jobs** tab: select them and
   click **Trim & stitch**.

Progress is saved as you go, so you can stop and come back any time. It's
also kept in a `backlog.json` file in the folder, so the folder can be
copied to another computer running the app.

### Editor keys

| Key | Does |
|:--|:--|
| Space or K | Play or pause |
| L | Play; press again to play faster |
| J | Play slower |
| Left, Right | Back or forward one frame (with Shift: one second) |
| Shift+J, Shift+L | Go to the start or end mark |
| I, O | Set the start or end at the playhead |
| + and - | Zoom the timeline in or out |
| Enter | Save (in Bulk edit: save and go to the next) |
| Esc | Back |

## Jobs

The **Jobs** tab lists every video with its marks, series and what's left
to do. Select jobs to trim, stitch or set their series together. **Import v1
file** brings in the old version's render state and bulk render lists.

## Settings

Besides OBS and ProPresenter, Settings has:

- **Marks:** padding added to the start and end of live marks.
- **Output folders** for trimmed clips and finished videos.
- **Rendering:** the video encoder (NVIDIA, Intel or AMD graphics are used
  when chosen, falling back to the processor if they can't run), quality,
  fast trim, loudness, and Subsplash's 1080p settings.

## Troubleshooting

- **OBS isn't connected.** Check that OBS is open, its WebSocket server is
  enabled, and the port and password on the Settings tab match. The app
  keeps trying every few seconds.
- **ProPresenter isn't connected.** Check the network and Stage App settings
  in ProPresenter, and that the password is the Stage App password.
- **Something else went wrong.** The app's log is in
  `%APPDATA%\SubsplashGenerator\logs\app.log`.

To uninstall, use **Settings**, **Apps** in Windows. Your settings and jobs
stay in `%APPDATA%\SubsplashGenerator` in case you reinstall.

## License

Subsplash Generator is MIT licensed (see [LICENSE](LICENSE)). It includes
[ffmpeg](https://ffmpeg.org), a separate program licensed under the GNU GPL
version 3, built by [BtbN](https://github.com/BtbN/FFmpeg-Builds). Each
release has ffmpeg's source attached, with the build scripts that name the
exact version of every library in it. The app lists all the third-party
licenses under **Settings**, **About**.

## Development

Requires Go, Node.js, and ffmpeg for the tests.

```sh
packaging/fetch-ffmpeg.sh linux ffmpeg-bin      # the pinned ffmpeg build
export SG_FFMPEG_DIR=$PWD/ffmpeg-bin
go test ./...
(cd frontend && npm ci && npm run build)
go run ./cmd/sg trim -start 00:31:04 -end 01:12:40 recording.mkv trimmed.mp4
go run ./cmd/sg stitch -intro intro.png -outro outro.mp4 trimmed.mp4 final.mp4
```

The desktop app (`cmd/app`) is Windows only. It only runs the ffmpeg in an
`ffmpeg` folder next to it, never one on PATH; `SG_FFMPEG_DIR` overrides
that folder for development. `SubsplashGenerator.exe --self-test` checks a
copy works without opening a window.

The ffmpeg build is pinned in `packaging/ffmpeg.env`. To update it, pick a
newer month-end autobuild from BtbN and copy in its version and checksums.

### Releasing

Push a tag such as `v2.0.0`. The release workflow builds the installer,
runs the self-test before and after installing it, and drafts a GitHub
release with the installer and ffmpeg's source. Review the draft, then
publish it. Pull requests run the same build; the installer can be
downloaded from the run's artifacts.
