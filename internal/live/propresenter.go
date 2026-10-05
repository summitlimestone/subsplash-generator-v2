// Package live follows a service as it happens: OBS's recording and
// ProPresenter's slides, turning them into marks on a live job.
package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
)

// Slide is a slide ProPresenter showed.
type Slide struct {
	UID  string    `json:"uid"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// reconnectInterval is how soon a dropped connection is retried. The
// stage display socket drops routinely, so this stays short and fixed:
// a growing backoff could miss a slide change mid-service.
const reconnectInterval = 4 * time.Second

// runProPresenter follows ProPresenter's stage display socket (the
// protocol v1 used, which works with the church's ProPresenter), calling
// onSlide whenever the current slide changes and onState as the
// connection comes and goes, until ctx is done.
func runProPresenter(ctx context.Context, host string, port int, password string, onSlide func(Slide), onState func(connected bool, err error)) {
	url := "ws://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/stagedisplay"
	for ctx.Err() == nil {
		err := stageDisplay(ctx, url, password, onSlide, func() { onState(true, nil) })
		if ctx.Err() != nil {
			return
		}
		onState(false, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectInterval):
		}
	}
}

func stageDisplay(ctx context.Context, url, password string, onSlide func(Slide), onConnected func()) error {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, _, err := websocket.Dial(dialCtx, url, nil)
	cancel()
	if err != nil {
		return fmt.Errorf("couldn't connect: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	auth, _ := json.Marshal(map[string]any{"pwd": password, "ptl": 610, "acn": "ath"})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		return err
	}
	last := ""
	authed := false
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("connection dropped: %w", err)
		}
		var msg struct {
			Action string `json:"acn"`
			OK     *bool  `json:"ath"`
			Err    string `json:"err"`
			Items  []struct {
				Action string `json:"acn"`
				UID    string `json:"uid"`
				Text   string `json:"txt"`
			} `json:"ary"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.Action == "ath" {
			if msg.OK != nil && !*msg.OK {
				return errors.New("ProPresenter rejected the password (Stage App password)")
			}
			authed = true
			onConnected()
			continue
		}
		if !authed {
			// Older versions may skip the acknowledgement.
			authed = true
			onConnected()
		}
		for _, it := range msg.Items {
			if it.Action == "cs" && it.UID != last {
				last = it.UID
				onSlide(Slide{UID: it.UID, Text: it.Text, At: time.Now()})
			}
		}
	}
}

// Matches reports whether a slide is the one s describes: by UID when one
// is set (stable, and present even on slides without text), else by its
// text, exactly or as a regular expression. An empty s matches nothing.
func Matches(s jobs.Slide, slide Slide) bool {
	if s.UID != "" {
		return strings.EqualFold(s.UID, slide.UID)
	}
	if s.Text == "" {
		return false
	}
	if s.Match == "regex" {
		pattern := s.Text
		if !s.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		return err == nil && re.MatchString(slide.Text)
	}
	if s.CaseSensitive {
		return slide.Text == s.Text
	}
	return strings.EqualFold(slide.Text, s.Text)
}
