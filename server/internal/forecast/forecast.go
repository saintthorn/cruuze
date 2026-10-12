// Package forecast answers "when will this be done?" from what a team has
// actually completed, by simulation rather than from estimates: each trial
// replays the remaining work against weeks drawn at random from the team's
// own recent history. The spread of the trials is the answer: a date for
// each level of confidence, instead of one date nobody believes.
package forecast

import (
	"math"
	"math/rand/v2"
	"slices"
)

const (
	// Trials is how many times the remaining work is played out.
	Trials = 5000
	// MinWeeks of history are needed before forecasting at all.
	MinWeeks = 4
	// maxWeeks is where a trial gives up: work that isn't done in ten years
	// at the observed pace isn't getting done at that pace.
	maxWeeks = 520
	// Never is the outcome of a trial that didn't finish.
	Never = maxWeeks * 7
)

// Enough reports whether the history can support a forecast: a few weeks of
// it, with work completed in more than one of them.
func Enough(done []int) bool {
	total, active := 0, 0
	for _, n := range done {
		total += n
		if n > 0 {
			active++
		}
	}
	return len(done) >= MinWeeks && total >= 3 && active >= 2
}

// Simulate plays out the remaining work and returns the number of days each
// trial took, sorted ascending. done holds how many issues were completed
// in each recent week; added how many were added to the scope in each (nil
// for a scope that doesn't grow). A trial that never finishes is Never.
func Simulate(remaining int, done, added []int, rng *rand.Rand) []int {
	days := make([]int, Trials)
	for trial := range days {
		days[trial] = simulateOnce(remaining, done, added, rng)
	}
	slices.Sort(days)
	return days
}

func simulateOnce(remaining int, done, added []int, rng *rand.Rand) int {
	left := float64(remaining)
	for week := 0; week < maxWeeks; week++ {
		completed := float64(done[rng.IntN(len(done))])
		if len(added) > 0 {
			// New work keeps arriving while there is work left.
			left += float64(added[rng.IntN(len(added))])
		}
		if completed >= left {
			// Done partway through this week.
			return week*7 + int(math.Ceil(left/completed*7))
		}
		left -= completed
	}
	return Never
}

// Percentile returns the value below which the fraction q of the sorted
// outcomes fall.
func Percentile(sorted []int, q float64) int {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[min(len(sorted)-1, max(0, index))]
}

// ProbabilityBy returns the share of outcomes that finished within days.
func ProbabilityBy(sorted []int, days int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	within, _ := slices.BinarySearch(sorted, days+1)
	return float64(within) / float64(len(sorted))
}

// Calibrate checks the method against history. At each past week that has
// lookback weeks behind it and horizon weeks after it, it makes the forecast
// that would have been made then ("85% likely to complete at least N in the
// next horizon weeks") and looks at what happened. It returns how many such
// forecasts there were and how many held; a well-calibrated method holds
// about 85% of the time.
func Calibrate(done []int, lookback, horizon int, rng *rand.Rand) (windows, held int) {
	const trials = 1000
	for at := lookback; at+horizon <= len(done); at++ {
		history := done[at-lookback : at]
		if !Enough(history) {
			continue
		}
		totals := make([]int, trials)
		for trial := range totals {
			for range horizon {
				totals[trial] += history[rng.IntN(len(history))]
			}
		}
		slices.Sort(totals)
		floor := Percentile(totals, 0.15) // 85% of trials did at least this much
		actual := 0
		for _, n := range done[at : at+horizon] {
			actual += n
		}
		windows++
		if actual >= floor {
			held++
		}
	}
	return windows, held
}
