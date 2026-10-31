package businesshours

import (
	"testing"
	"time"
)

func TestValidation(t *testing.T) {
	for _, r := range [][]Interval{
		{{1, 540, 540}}, {{1, 1080, 480}}, {{7, 0, 10}}, {{-1, 0, 10}}, {{0, -1, 10}}, {{0, 0, 1441}}, {{0, 1440, 1440}}, {{1, 0, 100}, {1, 99, 200}}, {{1, 0, 10}, {1, 0, 10}},
	} {
		if Validate(Config{Intervals: r}) == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	for _, r := range [][]Interval{nil, {}, {{0, 0, 1440}}, {{1, 0, 100}, {1, 100, 200}}, {{1, 1380, 1440}, {2, 0, 60}}} {
		if e := Validate(Config{Intervals: r}); e != nil {
			t.Fatal(e)
		}
	}
}
func TestBoundariesAndDST(t *testing.T) {
	tests := []struct {
		name, zone, instant, next string
		ranges                    []Interval
		open                      bool
	}{
		{"before", "America/Sao_Paulo", "2026-11-02T11:59:59Z", "2026-11-02T12:00:00Z", []Interval{{1, 540, 720}, {1, 780, 1080}}, false},
		{"start", "America/Sao_Paulo", "2026-11-02T12:00:00Z", "", []Interval{{1, 540, 720}}, true},
		{"lunch", "America/Sao_Paulo", "2026-11-02T15:00:00Z", "2026-11-02T16:00:00Z", []Interval{{1, 540, 720}, {1, 780, 1080}}, false},
		{"end-week", "UTC", "2026-11-02T12:00:00Z", "2026-11-09T09:00:00Z", []Interval{{1, 540, 720}}, false},
		{"midnight", "UTC", "2026-11-02T00:00:00Z", "2026-11-08T00:00:00Z", []Interval{{0, 0, 1440}}, false},
		{"empty", "UTC", "2026-11-02T00:00:00Z", "", nil, false},
		{"spring-gap", "America/New_York", "2026-03-08T06:59:59Z", "2026-03-15T06:15:00Z", []Interval{{0, 135, 165}}, false},
		{"spring-start", "America/New_York", "2026-03-08T06:59:59Z", "2026-03-08T07:00:00Z", []Interval{{0, 150, 210}}, false},
		{"fall-first", "America/New_York", "2026-11-01T05:30:00Z", "", []Interval{{0, 90, 105}}, true},
		{"fall-second", "America/New_York", "2026-11-01T06:30:00Z", "", []Interval{{0, 90, 105}}, true},
		{"fall-next", "America/New_York", "2026-11-01T05:45:00Z", "2026-11-01T06:30:00Z", []Interval{{0, 90, 105}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instant, e := time.Parse(time.RFC3339, tt.instant)
			if e != nil {
				t.Fatal(e)
			}
			c := Configuration{Configured: true, Timezone: tt.zone, Config: Config{Intervals: tt.ranges}}
			d, e := Evaluate(instant, c)
			if e != nil {
				t.Fatal(e)
			}
			next := ""
			if d.NextOpening != nil {
				next = d.NextOpening.Format(time.RFC3339)
			}
			if d.Open != tt.open || next != tt.next {
				t.Fatalf("open=%v next=%s", d.Open, next)
			}
			// The input's own location cannot change an instant-based decision.
			d2, e := Evaluate(instant.In(time.FixedZone("input", 11*3600)), c)
			if e != nil || d2.Open != d.Open {
				t.Fatalf("instant location: %+v %v", d2, e)
			}
		})
	}
	for _, zone := range []string{"", "Local", "bad/zone"} {
		if _, e := Evaluate(time.Now(), Configuration{Timezone: zone}); e == nil {
			t.Fatalf("accepted zone %s", zone)
		}
	}
	d, e := Evaluate(time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC), Configuration{Timezone: "UTC", Config: Config{Intervals: []Interval{{1, 0, 1440}}}})
	if e != nil || d.Open {
		t.Fatal("unconfigured must not invent business hours")
	}
}
