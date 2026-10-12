// Package metrics is a small registry of counters, histograms and gauges
// that writes the Prometheus text exposition format. It covers what this
// server needs without pulling in a client library.
package metrics

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
)

type collector interface {
	write(w io.Writer)
}

// Registry holds metrics and writes them out together.
type Registry struct {
	mu         sync.Mutex
	collectors []collector
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) register(c collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collectors = append(r.collectors, c)
}

// Write renders every metric in registration order.
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	collectors := slices.Clone(r.collectors)
	r.mu.Unlock()
	for _, c := range collectors {
		c.write(w)
	}
}

// Counter is a family of monotonically increasing values, one per
// combination of label values.
type Counter struct {
	name, help string
	labels     []string

	mu     sync.Mutex
	values map[string]float64
}

// Counter registers a counter. Keep label values to small, fixed sets: each
// distinct combination is a series that lives for the life of the process.
func (r *Registry) Counter(name, help string, labels ...string) *Counter {
	c := &Counter{name: name, help: help, labels: labels, values: map[string]float64{}}
	r.register(c)
	return c
}

// Inc adds one to the series with the given label values.
func (c *Counter) Inc(labelValues ...string) {
	key := seriesKey(c.labels, labelValues)
	c.mu.Lock()
	c.values[key]++
	c.mu.Unlock()
}

func (c *Counter) write(w io.Writer) {
	header(w, c.name, c.help, "counter")
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range sortedKeys(c.values) {
		fmt.Fprintf(w, "%s%s %s\n", c.name, braces(key), formatFloat(c.values[key]))
	}
}

// Histogram is a family of distributions over fixed buckets.
type Histogram struct {
	name, help string
	labels     []string
	buckets    []float64 // upper bounds, ascending

	mu     sync.Mutex
	series map[string]*distribution
}

type distribution struct {
	counts []uint64 // per bucket, not cumulative
	sum    float64
	count  uint64
}

// Histogram registers a histogram with the given bucket upper bounds.
func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *Histogram {
	h := &Histogram{name: name, help: help, labels: labels, buckets: buckets, series: map[string]*distribution{}}
	r.register(h)
	return h
}

// Observe records one value in the series with the given label values.
func (h *Histogram) Observe(value float64, labelValues ...string) {
	key := seriesKey(h.labels, labelValues)
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.series[key]
	if d == nil {
		d = &distribution{counts: make([]uint64, len(h.buckets))}
		h.series[key] = d
	}
	for i, bound := range h.buckets {
		if value <= bound {
			d.counts[i]++
			break
		}
	}
	d.sum += value
	d.count++
}

func (h *Histogram) write(w io.Writer) {
	header(w, h.name, h.help, "histogram")
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, key := range sortedKeys(h.series) {
		d := h.series[key]
		var cumulative uint64
		for i, bound := range h.buckets {
			cumulative += d.counts[i]
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, braces(join(key, `le="`+formatFloat(bound)+`"`)), cumulative)
		}
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, braces(join(key, `le="+Inf"`)), d.count)
		fmt.Fprintf(w, "%s_sum%s %s\n", h.name, braces(key), formatFloat(d.sum))
		fmt.Fprintf(w, "%s_count%s %d\n", h.name, braces(key), d.count)
	}
}

type gauge struct {
	name, help string
	read       func() float64
}

// Gauge registers a value that is read when the metrics are written.
func (r *Registry) Gauge(name, help string, read func() float64) {
	r.register(&gauge{name: name, help: help, read: read})
}

func (g *gauge) write(w io.Writer) {
	header(w, g.name, g.help, "gauge")
	fmt.Fprintf(w, "%s %s\n", g.name, formatFloat(g.read()))
}

func header(w io.Writer, name, help, kind string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// seriesKey renders label pairs the way they appear between the braces,
// which doubles as the map key for the series.
func seriesKey(names, values []string) string {
	if len(names) != len(values) {
		panic(fmt.Sprintf("metrics: got %d label values for %d labels", len(values), len(names)))
	}
	pairs := make([]string, len(names))
	for i, name := range names {
		pairs[i] = name + `="` + labelEscaper.Replace(values[i]) + `"`
	}
	return strings.Join(pairs, ",")
}

func join(key, extra string) string {
	if key == "" {
		return extra
	}
	return key + "," + extra
}

func braces(key string) string {
	if key == "" {
		return ""
	}
	return "{" + key + "}"
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
