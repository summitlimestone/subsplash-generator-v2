// Command app is the desktop app: the core (store, render queue, HTTP
// server) plus a window showing the web UI it serves.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/summitlimestone/subsplash-generator-v2/frontend"
	"github.com/summitlimestone/subsplash-generator-v2/internal/appdir"
	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
	"github.com/summitlimestone/subsplash-generator-v2/internal/live"
	"github.com/summitlimestone/subsplash-generator-v2/internal/mediacache"
	"github.com/summitlimestone/subsplash-generator-v2/internal/queue"
	"github.com/summitlimestone/subsplash-generator-v2/internal/server"
	"github.com/summitlimestone/subsplash-generator-v2/internal/store"
)

const appName = "Subsplash Generator"

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--self-test" {
		if !selfTest(os.Stdout) {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fatal(err)
	}
}

func run() error {
	dataDir, err := appdir.Data()
	if err != nil {
		return err
	}
	logFile, err := openLog(dataDir)
	if err != nil {
		return err
	}
	defer logFile.Close()
	log := slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("starting", "version", version, "data", dataDir)

	tools, err := ffmpeg.Locate()
	if err != nil {
		return fmt.Errorf("%w\n\nThe app needs its \"ffmpeg\" folder next to it. Reinstalling the app puts it back.", err)
	}
	st, err := store.Open(appdir.Database(dataDir))
	if err != nil {
		return err
	}
	defer st.Close()
	if ids, err := st.RecoverInterrupted(); err != nil {
		return err
	} else if len(ids) > 0 {
		log.Warn("jobs interrupted by the last shutdown", "ids", ids)
	}
	set, err := st.Settings()
	if err != nil {
		return err
	}
	if set.API.Token == "" {
		set.API.Token = newToken()
		if err := st.SaveSettings(set); err != nil {
			return err
		}
	}

	ui, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		return err
	}
	if _, err := fs.Stat(ui, "index.html"); err != nil {
		ui = nil
	}
	cache := mediacache.New(filepath.Join(dataDir, "cache"), tools, log)
	srv := &server.Server{Store: st, Tools: tools, Media: cache, Token: set.API.Token, UI: ui, Log: log, Version: version, Notices: notices()}
	onEvent := srv.Init()
	q := queue.New(st, tools, log, onEvent)
	srv.Queue = q
	cache.OnPeaks = srv.PeaksUpdated
	srv.NewToken = newToken
	onLive, onLiveJob := srv.LiveEvents()
	liveMgr := &live.Manager{Store: st, Dial: live.DialGoobs, OnChange: onLive, OnJob: onLiveJob}
	srv.Live = liveMgr

	host := set.API.Host
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, fmt.Sprint(set.API.Port)))
	if err != nil {
		return fmt.Errorf("couldn't listen on port %d (is another copy of the app running?): %w", set.API.Port, err)
	}
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.Run(ctx)
	go liveMgr.Run(ctx)

	app := application.New(application.Options{
		Name:           appName,
		SingleInstance: &application.SingleInstanceOptions{UniqueID: "org.summitlimestone.subsplash-generator"},
		Assets: application.AssetOptions{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "loading", http.StatusNotFound)
		})},
		Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(dataDir, "webview"), AdditionalBrowserArgs: devtoolsArgs()},
	})
	srv.OpenFile = func(title string, patterns []string) (string, error) {
		d := app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(true)
		if len(patterns) > 0 {
			d.AddFilter("Videos", strings.Join(patterns, ";"))
		}
		return d.PromptForSingleSelection()
	}
	srv.OpenFolder = func(title string) (string, error) {
		d := app.Dialog.OpenFile().SetTitle(title).CanChooseDirectories(true).CanChooseFiles(false)
		if dir := startFolder(st); dir != "" {
			d.SetDirectory(dir)
		}
		return d.PromptForSingleSelection()
	}
	// The minimum size is small enough to sit beside OBS showing only Live.
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            appName,
		Width:            1400,
		Height:           900,
		MinWidth:         580,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(0x20, 0x1e, 0x1e),
		URL:              fmt.Sprintf("http://127.0.0.1:%d/?token=%s", set.API.Port, set.API.Token),
	})
	err = app.Run()

	cancel()
	shutdown, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	_ = httpSrv.Shutdown(shutdown)
	log.Info("stopped")
	return err
}

// startFolder is where the folder picker opens: next to the last backlog
// opened, else the user's Videos folder.
func startFolder(st *store.Store) string {
	if set, err := st.Settings(); err == nil && len(set.Backlogs) > 0 {
		return filepath.Dir(set.Backlogs[len(set.Backlogs)-1])
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Videos")
	}
	return ""
}

// notices is the path of the third-party license notices installed next
// to the program, or "" when there are none (a development build).
func notices() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(exe), "THIRD_PARTY_NOTICES.txt")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// devtoolsArgs opens the webview to Chrome DevTools on SG_DEVTOOLS_PORT,
// for development only.
func devtoolsArgs() []string {
	if port := os.Getenv("SG_DEVTOOLS_PORT"); port != "" {
		return []string{"--remote-debugging-port=" + port, "--remote-allow-origins=*"}
	}
	return nil
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// openLog appends to the app's log, starting afresh once it passes 5 MB.
func openLog(dataDir string) (io.WriteCloser, error) {
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "app.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(path, path+".old")
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}
