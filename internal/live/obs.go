package live

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/events"
	"github.com/andreykaipov/goobs/api/events/subscriptions"
)

// RecordEvent is a change in OBS's recording.
type RecordEvent struct {
	Recording bool
	// Path is the finished file, set when a recording stops.
	Path string
}

// RecordStatus is OBS's recording state at one moment.
type RecordStatus struct {
	Active bool
	Paused bool
	// Elapsed is how far into the file the recording is, excluding pauses:
	// exactly the time a mark needs.
	Elapsed time.Duration
}

// OBS is a connection to OBS. Events closes when the connection drops.
type OBS interface {
	Events() <-chan RecordEvent
	RecordStatus() (RecordStatus, error)
	RecordDirectory() (string, error)
	Close()
}

// DialOBS connects to OBS's websocket server.
type DialOBS func(host string, port int, password string) (OBS, error)

// DialGoobs connects with goobs, the obs-websocket v5 client.
func DialGoobs(host string, port int, password string) (OBS, error) {
	c, err := goobs.New(net.JoinHostPort(host, strconv.Itoa(port)),
		goobs.WithPassword(password),
		goobs.WithEventSubscriptions(subscriptions.Outputs),
	)
	if err != nil {
		return nil, fmt.Errorf("couldn't connect to OBS at %s:%d: %w", host, port, err)
	}
	o := &goobsOBS{c: c, events: make(chan RecordEvent, 16)}
	go o.pump()
	return o, nil
}

type goobsOBS struct {
	c      *goobs.Client
	events chan RecordEvent
}

func (o *goobsOBS) pump() {
	defer close(o.events)
	for ev := range o.c.IncomingEvents {
		if e, ok := ev.(*events.RecordStateChanged); ok {
			switch e.OutputState {
			case "OBS_WEBSOCKET_OUTPUT_STARTED":
				o.events <- RecordEvent{Recording: true}
			case "OBS_WEBSOCKET_OUTPUT_STOPPED":
				o.events <- RecordEvent{Recording: false, Path: e.OutputPath}
			}
		}
	}
}

func (o *goobsOBS) Events() <-chan RecordEvent { return o.events }

func (o *goobsOBS) RecordStatus() (RecordStatus, error) {
	r, err := o.c.Record.GetRecordStatus()
	if err != nil {
		return RecordStatus{}, err
	}
	return RecordStatus{
		Active: r.OutputActive, Paused: r.OutputPaused,
		Elapsed: time.Duration(r.OutputDuration * float64(time.Millisecond)),
	}, nil
}

func (o *goobsOBS) RecordDirectory() (string, error) {
	r, err := o.c.Config.GetRecordDirectory()
	if err != nil {
		return "", err
	}
	return r.RecordDirectory, nil
}

func (o *goobsOBS) Close() { _ = o.c.Disconnect() }
