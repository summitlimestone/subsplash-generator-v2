package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/summitlimestone/subsplash-generator-v2/internal/jobs"
	"github.com/summitlimestone/subsplash-generator-v2/internal/live"
	"github.com/summitlimestone/subsplash-generator-v2/internal/v1import"
)

// LiveEvents returns the callbacks the live manager reports through.
func (s *Server) LiveEvents() (onChange func(live.State), onJob func(*jobs.Job)) {
	return func(st live.State) { s.hub.publish("live", st) },
		func(j *jobs.Job) { s.hub.publish("job", j) }
}

func (s *Server) liveRoutes(api func(string, func(http.ResponseWriter, *http.Request) (any, error))) {
	api("GET /api/live", func(http.ResponseWriter, *http.Request) (any, error) { return s.liveOrErr() })
	api("POST /api/live/start", s.liveStart)
	api("POST /api/live/stop", func(http.ResponseWriter, *http.Request) (any, error) {
		if s.Live == nil {
			return nil, errNoLive
		}
		s.Live.Stop()
		return s.Live.State(), nil
	})
	api("POST /api/live/mark", s.liveMark)
	api("POST /api/live/nudge", s.liveNudge)
	api("PUT /api/live/series", s.liveSeries)
	api("PUT /api/settings", s.putSettings)
	api("POST /api/settings/token", s.regenerateToken)
	api("GET /api/settings/dock", s.dockURLs)
	api("POST /api/series", s.createSeries)
	api("PUT /api/series/{name}", s.updateSeries)
	api("DELETE /api/series/{name}", s.deleteSeries)
	api("POST /api/import/v1", s.importV1)
	api("POST /api/welcome", func(http.ResponseWriter, *http.Request) (any, error) {
		_, err := s.Store.UpdateSettings(func(set *jobs.Settings) { set.Welcomed = true })
		return nil, err
	})
}

var errNoLive = httpError{http.StatusServiceUnavailable, "live mode isn't running"}

func (s *Server) liveOrErr() (any, error) {
	if s.Live == nil {
		return nil, errNoLive
	}
	return s.Live.State(), nil
}

func liveError(err error) error {
	if errors.Is(err, live.ErrNotApplicable) {
		return httpError{http.StatusConflict, err.Error()}
	}
	return err
}

func (s *Server) liveStart(_ http.ResponseWriter, r *http.Request) (any, error) {
	if s.Live == nil {
		return nil, errNoLive
	}
	var req struct {
		Series string `json:"series"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := s.Live.Start(req.Series); err != nil {
		return nil, liveError(err)
	}
	return s.Live.State(), nil
}

func (s *Server) liveMark(_ http.ResponseWriter, r *http.Request) (any, error) {
	if s.Live == nil {
		return nil, errNoLive
	}
	var req struct {
		Which string `json:"which"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Which != "start" && req.Which != "end" {
		return nil, badRequest("which must be start or end")
	}
	if err := s.Live.Mark(req.Which); err != nil {
		return nil, liveError(err)
	}
	return s.Live.State(), nil
}

func (s *Server) liveNudge(_ http.ResponseWriter, r *http.Request) (any, error) {
	if s.Live == nil {
		return nil, errNoLive
	}
	var req struct {
		Which   string  `json:"which"`
		Seconds float64 `json:"seconds"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Which != "start" && req.Which != "end" {
		return nil, badRequest("which must be start or end")
	}
	if err := s.Live.Nudge(req.Which, req.Seconds); err != nil {
		return nil, liveError(err)
	}
	return s.Live.State(), nil
}

func (s *Server) liveSeries(_ http.ResponseWriter, r *http.Request) (any, error) {
	if s.Live == nil {
		return nil, errNoLive
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := s.Live.SetSeries(req.Name); err != nil {
		return nil, err
	}
	return s.Live.State(), nil
}

// settingsView is the settings as the UI sees them: passwords and the
// API token are never sent back, only whether they are set.
type settingsView struct {
	jobs.Settings
	HasOBSPassword bool `json:"hasOBSPassword"`
	HasPPPassword  bool `json:"hasPPPassword"`
}

func (s *Server) settings(http.ResponseWriter, *http.Request) (any, error) {
	set, err := s.Store.Settings()
	if err != nil {
		return nil, err
	}
	v := settingsView{Settings: set, HasOBSPassword: set.OBS.Password != "", HasPPPassword: set.ProPresenter.Password != ""}
	v.OBS.Password, v.ProPresenter.Password, v.API.Token = "", "", ""
	return v, nil
}

// putSettings replaces the settings. A blank password keeps the saved one
// unless clearOBSPassword/clearPPPassword is set; the token, backlog
// folders and welcome flag are managed elsewhere and kept.
func (s *Server) putSettings(_ http.ResponseWriter, r *http.Request) (any, error) {
	var req struct {
		jobs.Settings
		ClearOBSPassword bool `json:"clearOBSPassword"`
		ClearPPPassword  bool `json:"clearPPPassword"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	in := req.Settings
	if err := validateSettings(in); err != nil {
		return nil, err
	}
	var restart bool
	_, err := s.Store.UpdateSettings(func(set *jobs.Settings) {
		restart = set.API.Host != in.API.Host || set.API.Port != in.API.Port
		if in.OBS.Password == "" && !req.ClearOBSPassword {
			in.OBS.Password = set.OBS.Password
		}
		if in.ProPresenter.Password == "" && !req.ClearPPPassword {
			in.ProPresenter.Password = set.ProPresenter.Password
		}
		in.API.Token, in.Backlogs, in.Welcomed = set.API.Token, set.Backlogs, set.Welcomed
		*set = in
	})
	if err != nil {
		return nil, err
	}
	if s.Live != nil {
		s.Live.Reconfigure()
	}
	v, err := s.settings(nil, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"settings": v, "restartNeeded": restart}, nil
}

func validateSettings(set jobs.Settings) error {
	for _, p := range []struct {
		name string
		port int
	}{{"OBS", set.OBS.Port}, {"ProPresenter", set.ProPresenter.Port}, {"API", set.API.Port}} {
		if p.port < 1 || p.port > 65535 {
			return badRequest("the %s port must be between 1 and 65535", p.name)
		}
	}
	for name, sl := range map[string]jobs.Slide{"start": set.ProPresenter.BeginSlide, "end": set.ProPresenter.EndSlide} {
		if sl.Match == "regex" && sl.UID == "" {
			if _, err := regexp.Compile(sl.Text); err != nil {
				return badRequest("the sermon %s slide's pattern isn't valid: %v", name, err)
			}
		}
	}
	if set.TrimmedDir == "" || set.FinalDir == "" {
		return badRequest("both output folders are required")
	}
	if set.Render.TrimCRF < 0 || set.Render.TrimCRF > 51 || set.Render.StitchCRF < 0 || set.Render.StitchCRF > 51 {
		return badRequest("quality (CRF) must be between 0 and 51")
	}
	if set.API.Host != "127.0.0.1" && set.API.Host != "0.0.0.0" {
		return badRequest("the API listens on 127.0.0.1 (this computer) or 0.0.0.0 (the network)")
	}
	return nil
}

// regenerateToken makes a new API token. Docks and scripts using the old
// one stop working, and the app must restart to use it.
func (s *Server) regenerateToken(http.ResponseWriter, *http.Request) (any, error) {
	if s.NewToken == nil {
		return nil, httpError{http.StatusNotImplemented, "tokens are managed by the app"}
	}
	if _, err := s.Store.UpdateSettings(func(set *jobs.Settings) { set.API.Token = s.NewToken() }); err != nil {
		return nil, err
	}
	return map[string]bool{"restartNeeded": true}, nil
}

// dockURLs are the addresses to paste into OBS's Custom Browser Docks:
// this computer's, and with LAN access on, the network ones.
func (s *Server) dockURLs(http.ResponseWriter, *http.Request) (any, error) {
	set, err := s.Store.Settings()
	if err != nil {
		return nil, err
	}
	port := strconv.Itoa(set.API.Port)
	url := func(host string) string {
		return "http://" + net.JoinHostPort(host, port) + "/dock?token=" + set.API.Token
	}
	urls := []string{url("127.0.0.1")}
	if set.API.Host == "0.0.0.0" {
		for _, ip := range lanAddresses() {
			urls = append(urls, url(ip))
		}
	}
	return map[string]any{"urls": urls}, nil
}

func lanAddresses() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

func (s *Server) createSeries(_ http.ResponseWriter, r *http.Request) (any, error) {
	var sr jobs.Series
	if err := decode(r, &sr); err != nil {
		return nil, err
	}
	if err := validateSeries(&sr); err != nil {
		return nil, err
	}
	if _, err := s.Store.FindSeries(sr.Name); err == nil {
		return nil, httpError{http.StatusConflict, fmt.Sprintf("there's already a series called %q", sr.Name)}
	}
	if err := s.Store.SaveSeries(sr); err != nil {
		return nil, err
	}
	s.publishAll()
	return sr, nil
}

// updateSeries saves a series, carrying a rename over to every job that
// uses it.
func (s *Server) updateSeries(_ http.ResponseWriter, r *http.Request) (any, error) {
	old := r.PathValue("name")
	var sr jobs.Series
	if err := decode(r, &sr); err != nil {
		return nil, err
	}
	if err := validateSeries(&sr); err != nil {
		return nil, err
	}
	if _, err := s.Store.FindSeries(old); err != nil {
		return nil, err
	}
	if sr.Name != old {
		if _, err := s.Store.FindSeries(sr.Name); err == nil {
			return nil, httpError{http.StatusConflict, fmt.Sprintf("there's already a series called %q", sr.Name)}
		}
	}
	if err := s.Store.SaveSeries(sr); err != nil {
		return nil, err
	}
	if sr.Name != old {
		all, err := s.Store.Jobs()
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, j := range all {
			if j.Series == old {
				ids = append(ids, j.ID)
			}
		}
		if len(ids) > 0 {
			name := sr.Name
			if _, err := s.edit(ids, jobEdit{Series: &name}); err != nil {
				return nil, fmt.Errorf("renamed, but some jobs still use the old name: %w", err)
			}
		}
		if err := s.Store.DeleteSeries(old); err != nil {
			return nil, err
		}
	}
	s.publishAll()
	return sr, nil
}

// deleteSeries removes a series. One that jobs still use is only deleted
// with ?force=1, so the UI can ask first.
func (s *Server) deleteSeries(_ http.ResponseWriter, r *http.Request) (any, error) {
	name := r.PathValue("name")
	all, err := s.Store.Jobs()
	if err != nil {
		return nil, err
	}
	used := 0
	for _, j := range all {
		if j.Series == name && j.Status != jobs.StatusDone {
			used++
		}
	}
	if used > 0 && r.URL.Query().Get("force") != "1" {
		return nil, httpError{http.StatusConflict, fmt.Sprintf("%d unfinished job(s) use %q", used, name)}
	}
	if err := s.Store.DeleteSeries(name); err != nil {
		return nil, err
	}
	s.publishAll()
	return map[string]bool{"ok": true}, nil
}

func validateSeries(sr *jobs.Series) error {
	switch {
	case sr.Name == "":
		return badRequest("a series needs a name")
	case sr.Intro == "" || sr.Outro == "":
		return badRequest("a series needs an intro and an outro")
	case sr.IntroDuration < 0 || sr.OutroDuration < 0:
		return badRequest("image durations can't be negative")
	}
	if sr.Transition == "" {
		sr.Transition = "fade"
	}
	if sr.TransitionDuration <= 0 {
		return badRequest("the transition needs a length above zero")
	}
	return nil
}

// importV1 brings in v1's config.json and series.json from where v1 kept
// them, as far as they exist.
func (s *Server) importV1(http.ResponseWriter, *http.Request) (any, error) {
	dir := v1import.Dir()
	result := map[string]any{"folder": dir, "settings": false, "series": 0}
	if set, err := s.Store.Settings(); err == nil {
		if updated, err := v1import.Config(filepath.Join(dir, "config.json"), set); err == nil {
			if err := s.Store.SaveSettings(updated); err != nil {
				return nil, err
			}
			result["settings"] = true
			if s.Live != nil {
				s.Live.Reconfigure()
			}
		}
	}
	if list, err := v1import.Series(filepath.Join(dir, "series.json")); err == nil {
		for _, sr := range list {
			if err := s.Store.SaveSeries(sr); err != nil {
				return nil, err
			}
		}
		result["series"] = len(list)
	}
	s.publishAll()
	return result, nil
}
