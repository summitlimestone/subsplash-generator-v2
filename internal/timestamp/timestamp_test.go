package timestamp

import "testing"

func TestFormat(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want string
	}{
		{0, "00:00:00.000"},
		{3725.5, "01:02:05.500"},
		{59.9996, "00:01:00.000"},
		{-1.25, "-00:00:01.250"},
		{36000, "10:00:00.000"},
	} {
		if got := Format(c.in); got != c.want {
			t.Errorf("Format(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want float64
	}{
		{"00:00:00.000", 0},
		{"01:02:05.5", 3725.5},
		{" 00:31:04.250 ", 1864.25},
		{"-00:00:01.250", -1.25},
		{"100:00:00", 360000},
	} {
		got, err := Parse(c.in)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "1:2:3", "00:60:00", "00:00:60", "abc", "00:00:00.", "12.5"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", bad)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, s := range []float64{0, 0.001, 1.5, 4321.987, 7199.999} {
		got, err := Parse(Format(s))
		if err != nil || got != s {
			t.Errorf("round trip of %v gave %v, %v", s, got, err)
		}
	}
}
