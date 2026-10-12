package metrics

import (
	"strings"
	"testing"
)

func TestWrite(t *testing.T) {
	r := NewRegistry()
	requests := r.Counter("http_requests_total", "Requests.", "method", "status")
	requests.Inc("GET", "200")
	requests.Inc("GET", "200")
	requests.Inc("POST", "500")
	plain := r.Counter("emails_total", "Emails.")
	plain.Inc()

	latency := r.Histogram("latency_seconds", "Latency.", []float64{0.1, 1}, "route")
	latency.Observe(0.05, "/a")
	latency.Observe(0.5, "/a")
	latency.Observe(30, "/a")
	latency.Observe(1, `/b"x`)

	r.Gauge("sockets", "Open sockets.", func() float64 { return 3 })

	var out strings.Builder
	r.Write(&out)
	want := `# HELP http_requests_total Requests.
# TYPE http_requests_total counter
http_requests_total{method="GET",status="200"} 2
http_requests_total{method="POST",status="500"} 1
# HELP emails_total Emails.
# TYPE emails_total counter
emails_total 1
# HELP latency_seconds Latency.
# TYPE latency_seconds histogram
latency_seconds_bucket{route="/a",le="0.1"} 1
latency_seconds_bucket{route="/a",le="1"} 2
latency_seconds_bucket{route="/a",le="+Inf"} 3
latency_seconds_sum{route="/a"} 30.55
latency_seconds_count{route="/a"} 3
latency_seconds_bucket{route="/b\"x",le="0.1"} 0
latency_seconds_bucket{route="/b\"x",le="1"} 1
latency_seconds_bucket{route="/b\"x",le="+Inf"} 1
latency_seconds_sum{route="/b\"x"} 1
latency_seconds_count{route="/b\"x"} 1
# HELP sockets Open sockets.
# TYPE sockets gauge
sockets 3
`
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestLabelCountMismatchPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected a panic")
		}
	}()
	NewRegistry().Counter("x", "X.", "a").Inc()
}
