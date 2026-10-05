// Package mediacache makes and caches what the trim editor shows besides
// the video itself: the audio waveform and filmstrip thumbnails.
package mediacache

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/summitlimestone/subsplash-generator-v2/internal/ffmpeg"
)

// PeaksPerSecond is the waveform's resolution: one level per 50 ms.
const PeaksPerSecond = 20

const peakRate = 8000 // Hz the audio is decoded at for peaks

// Cache makes waveforms and thumbnails, keeping them in dir.
type Cache struct {
	dir   string
	tools ffmpeg.Tools
	log   *slog.Logger
	// OnPeaks, if set, is called as a waveform grows.
	OnPeaks func(path string)

	mu    sync.Mutex
	peaks map[string]*peakJob
	thumb chan struct{} // limits concurrent thumbnail extractions
}

type peakJob struct {
	mu   sync.Mutex
	data []byte // one byte per peak, 0-255
	done bool
	err  error
}

// New returns a cache that keeps its files in dir.
func New(dir string, tools ffmpeg.Tools, log *slog.Logger) *Cache {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Cache{dir: dir, tools: tools, log: log, peaks: map[string]*peakJob{}, thumb: make(chan struct{}, 2)}
}

// formatVersion changes whenever cached files change format.
const formatVersion = "2"

// key identifies a file's current contents by path, size and mtime.
func key(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%d|%d", formatVersion, path, st.Size(), st.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:10]), nil
}

// Peaks returns the waveform of path's audio as computed so far, and
// whether it is complete. The first call starts computing it in the
// background; a finished waveform is cached on disk.
func (c *Cache) Peaks(path string) (data []byte, complete bool, err error) {
	k, err := key(path)
	if err != nil {
		return nil, false, err
	}
	file := filepath.Join(c.dir, k+".peaks")
	if b, err := os.ReadFile(file); err == nil {
		return b, true, nil
	}
	c.mu.Lock()
	job, ok := c.peaks[k]
	if !ok {
		job = &peakJob{}
		c.peaks[k] = job
		go c.makePeaks(path, file, job)
	}
	c.mu.Unlock()
	job.mu.Lock()
	defer job.mu.Unlock()
	return append([]byte(nil), job.data...), job.done, job.err
}

func (c *Cache) makePeaks(path, file string, job *peakJob) {
	err := c.decodePeaks(path, job)
	job.mu.Lock()
	job.done, job.err = true, err
	data := job.data
	job.mu.Unlock()
	if err != nil {
		c.log.Warn("waveform failed", "file", path, "err", err)
	} else if err := os.MkdirAll(c.dir, 0o755); err == nil {
		_ = os.WriteFile(file, data, 0o644)
	}
	if c.OnPeaks != nil {
		c.OnPeaks(path)
	}
}

// decodePeaks streams the audio as 16-bit mono PCM and stores each
// bucket's RMS level on a 60 dB scale. Loudness, unlike peaks, tells dense
// music apart from speech and its pauses.
func (c *Cache) decodePeaks(path string, job *peakJob) error {
	cmd := exec.Command(c.tools.FFmpeg, "-hide_banner", "-nostdin", "-loglevel", "error",
		"-i", path, "-map", "0:a:0", "-vn", "-ac", "1", "-ar", fmt.Sprint(peakRate), "-f", "s16le", "-")
	ffmpeg.HideWindow(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	const perPeak = peakRate / PeaksPerSecond
	buf := make([]byte, 64<<10)
	var carry []byte // an odd trailing byte from the previous read
	var sum float64
	n := 0
	var batch []byte
	flush := func() {
		job.mu.Lock()
		job.data = append(job.data, batch...)
		job.mu.Unlock()
		batch = batch[:0]
		if c.OnPeaks != nil {
			c.OnPeaks(path)
		}
	}
	for {
		m, rerr := out.Read(buf)
		chunk := append(carry, buf[:m]...)
		i := 0
		for ; i+1 < len(chunk); i += 2 {
			v := float64(int16(binary.LittleEndian.Uint16(chunk[i:]))) / 32768
			sum += v * v
			if n++; n == perPeak {
				batch = append(batch, level(math.Sqrt(sum/perPeak)))
				sum, n = 0, 0
				if len(batch) == PeaksPerSecond*60 { // publish a minute at a time
					flush()
				}
			}
		}
		carry = append(carry[:0], chunk[i:]...)
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				return rerr
			}
			break
		}
	}
	flush()
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("decoding audio: %w", err)
	}
	return nil
}

// level maps an RMS amplitude to 0-255 over -60 to 0 dBFS.
func level(rms float64) byte {
	if rms <= 0 {
		return 0
	}
	db := 20 * math.Log10(rms)
	return byte(math.Round(255 * math.Max(0, math.Min(1, (db+60)/60))))
}

// Thumbnail returns a JPEG of the frame at seconds into path, height
// pixels tall, extracting and caching it on first use.
func (c *Cache) Thumbnail(ctx context.Context, path string, seconds float64, height int) (string, error) {
	k, err := key(path)
	if err != nil {
		return "", err
	}
	file := filepath.Join(c.dir, "thumbs", k, fmt.Sprintf("%d_%d.jpg", int(math.Round(seconds*10)), height))
	if _, err := os.Stat(file); err == nil {
		return file, nil
	}
	select {
	case c.thumb <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-c.thumb }()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	tmp := file + ".tmp.jpg"
	// A seek before -i lands on the nearest keyframe, which is plenty
	// for a filmstrip and far faster than decoding to the exact frame.
	err = c.tools.Run(ctx, []string{
		"-y", "-ss", fmt.Sprintf("%.3f", seconds), "-noaccurate_seek", "-i", path,
		"-frames:v", "1", "-vf", fmt.Sprintf("scale=-2:%d", height), "-q:v", "5", tmp,
	}, nil)
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return file, os.Rename(tmp, file)
}
