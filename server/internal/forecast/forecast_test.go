package forecast

import (
	"math/rand/v2"
	"testing"
)

func rng() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestEnough(t *testing.T) {
	cases := []struct {
		done []int
		want bool
	}{
		{nil, false},
		{[]int{5, 5, 5}, false},    // too few weeks
		{[]int{0, 0, 0, 9}, false}, // one busy week is not a pace
		{[]int{1, 0, 1, 0}, false}, // too little done
		{[]int{2, 0, 1, 0}, true},
		{[]int{4, 6, 5, 7, 3}, true},
	}
	for _, c := range cases {
		if got := Enough(c.done); got != c.want {
			t.Errorf("Enough(%v) = %v, want %v", c.done, got, c.want)
		}
	}
}

func TestSimulateSteadyPace(t *testing.T) {
	// Exactly 5 a week, 20 to go: four weeks, every time.
	days := Simulate(20, []int{5, 5, 5, 5}, nil, rng())
	if p50, p95 := Percentile(days, 0.5), Percentile(days, 0.95); p50 != 28 || p95 != 28 {
		t.Errorf("steady pace: p50 = %d, p95 = %d days, want 28 and 28", p50, p95)
	}
	// Nothing left is done today.
	if days := Simulate(0, []int{5, 5, 5, 5}, nil, rng()); Percentile(days, 0.95) != 0 {
		t.Errorf("nothing remaining: p95 = %d days, want 0", Percentile(days, 0.95))
	}
}

func TestSimulateSpreadsWithUnevenHistory(t *testing.T) {
	days := Simulate(30, []int{0, 2, 9, 4, 12, 1, 6, 3}, nil, rng())
	p50, p85, p95 := Percentile(days, 0.5), Percentile(days, 0.85), Percentile(days, 0.95)
	if !(p50 < p85 && p85 < p95) {
		t.Fatalf("uneven history should give a spread: p50 %d, p85 %d, p95 %d", p50, p85, p95)
	}
	// The average pace is about 4.6 a week, so 30 issues is around 6.5 weeks.
	if p50 < 35 || p50 > 60 {
		t.Errorf("p50 = %d days, want roughly 45", p50)
	}
	if got := ProbabilityBy(days, p85); got < 0.85 || got > 0.9 {
		t.Errorf("probability of finishing by the p85 date = %.2f", got)
	}
	if got := ProbabilityBy(days, 7); got != 0 {
		t.Errorf("probability of finishing 30 issues in a week = %.2f, want 0", got)
	}
}

func TestSimulateWithGrowingScope(t *testing.T) {
	done := []int{5, 5, 5, 5}
	fixed := Percentile(Simulate(20, done, nil, rng()), 0.5)
	growing := Percentile(Simulate(20, done, []int{2, 2, 2, 2}, rng()), 0.5)
	if growing <= fixed {
		t.Errorf("scope growing by 2 a week: p50 = %d days, want later than %d", growing, fixed)
	}
	// Work arriving as fast as it is done never finishes.
	if got := Percentile(Simulate(20, done, []int{5, 6, 5, 6}, rng()), 0.5); got != Never {
		t.Errorf("scope outrunning the pace: p50 = %d, want Never", got)
	}
}

func TestCalibrate(t *testing.T) {
	// A steady team: every forecast holds.
	steady := []int{5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5}
	windows, held := Calibrate(steady, 8, 4, rng())
	if windows != 5 || held != 5 {
		t.Errorf("steady history: %d of %d held, want 5 of 5", held, windows)
	}
	// A team that stopped: the forecasts made before the stop were wrong.
	stopped := []int{6, 5, 7, 6, 5, 6, 7, 5, 0, 0, 0, 0}
	windows, held = Calibrate(stopped, 8, 4, rng())
	if windows != 1 || held != 0 {
		t.Errorf("history that stops: %d of %d held, want 0 of 1", held, windows)
	}
	// Too short to check anything.
	if windows, _ := Calibrate([]int{5, 5, 5}, 8, 4, rng()); windows != 0 {
		t.Errorf("short history gave %d windows", windows)
	}
}
