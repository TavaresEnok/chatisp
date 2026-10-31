package businesshours

import (
	"errors"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/google/uuid"
)

var (
	ErrInvalid  = errors.New("horários inválidos")
	ErrConflict = errors.New("versão ou comando conflitante")
)

// Interval uses local wall minutes, Sunday=0. End=1440 is midnight
// at the end of the day. Overnight ranges must be split across weekdays.
type Interval struct {
	Day   int `json:"day"`
	Start int `json:"start"`
	End   int `json:"end"`
}
type Config struct {
	Intervals       []Interval `json:"intervals"`
	Welcome         string     `json:"welcome"`
	OutsideHours    string     `json:"outside_hours"`
	NobodyAvailable string     `json:"nobody_available"`
	OnCallSectorID  *uuid.UUID `json:"on_call_sector_id"`
}
type Configuration struct {
	Version    int64  `json:"version"`
	Configured bool   `json:"configured"`
	Timezone   string `json:"timezone"`
	Config     Config `json:"config"`
}
type Decision struct {
	Configuration
	Instant             time.Time  `json:"instant"`
	Open                bool       `json:"open"`
	NextOpening         *time.Time `json:"next_opening"`
	DefaultSectorID     *uuid.UUID `json:"default_sector_id"`
	OnCallSectorID      *uuid.UUID `json:"on_call_sector_id"`
	DestinationSectorID *uuid.UUID `json:"destination_sector_id"`
}

func Validate(c Config) error {
	if len(c.Intervals) > 112 {
		return ErrInvalid
	}
	if c.OnCallSectorID != nil && *c.OnCallSectorID == uuid.Nil {
		return ErrInvalid
	}
	for _, s := range []string{c.Welcome, c.OutsideHours, c.NobodyAvailable} {
		if len([]rune(s)) > 4000 || strings.ContainsRune(s, 0) {
			return ErrInvalid
		}
	}
	ranges := append([]Interval(nil), c.Intervals...)
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].Day != ranges[j].Day {
			return ranges[i].Day < ranges[j].Day
		}
		return ranges[i].Start < ranges[j].Start
	})
	for i, r := range ranges {
		if r.Day < 0 || r.Day > 6 || r.Start < 0 || r.Start >= 1440 || r.End <= r.Start || r.End > 1440 {
			return ErrInvalid
		}
		if i > 0 && ranges[i-1].Day == r.Day && ranges[i-1].End > r.Start {
			return ErrInvalid
		}
	}
	return nil
}
func location(zone string) (*time.Location, error) {
	if zone == "" || zone == "Local" {
		return nil, ErrInvalid
	}
	l, e := time.LoadLocation(zone)
	if e != nil {
		return nil, ErrInvalid
	}
	return l, nil
}
func openAt(instant time.Time, loc *time.Location, ranges []Interval) bool {
	t := instant.In(loc)
	minute := t.Hour()*60 + t.Minute()
	for _, r := range ranges {
		if r.Day == int(t.Weekday()) && minute >= r.Start && minute < r.End {
			return true
		}
	}
	return false
}

// Evaluate is pure. Repeated wall minutes during DST are both eligible;
// nonexistent minutes never open. Search on the UTC timeline avoids Date's
// ambiguous/nonexistent local-time normalization. Results have minute precision.
func Evaluate(instant time.Time, c Configuration) (Decision, error) {
	d := Decision{Configuration: c, Instant: instant.UTC()}
	loc, e := location(c.Timezone)
	if e != nil {
		return d, e
	}
	if e = Validate(c.Config); e != nil {
		return d, e
	}
	if !c.Configured {
		return d, nil
	}
	d.Open = openAt(instant, loc, c.Config.Intervals)
	if !d.Open && len(c.Config.Intervals) > 0 {
		for t, limit := instant.UTC().Truncate(time.Minute).Add(time.Minute), instant.AddDate(0, 0, 9); t.Before(limit); t = t.Add(time.Minute) {
			if openAt(t, loc, c.Config.Intervals) {
				v := t
				d.NextOpening = &v
				break
			}
		}
	}
	return d, nil
}
