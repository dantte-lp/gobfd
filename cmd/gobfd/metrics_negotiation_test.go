package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dantte-lp/gobfd/internal/config"
)

func TestMetricsServerNegotiatesFormatsAndCompression(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "gobfd_negotiation_test", Help: "Negotiation probe."})
	reg.MustRegister(gauge)
	gauge.Set(1)
	srv := newMetricsServer(config.MetricsConfig{Path: "/metrics"}, reg)

	for _, test := range []struct {
		name        string
		accept      string
		compression string
		contentType string
	}{
		{name: "default text", contentType: "text/plain"},
		{name: "wildcard text", accept: "*/*", contentType: "text/plain"},
		{
			name: "protobuf", contentType: "application/vnd.google.protobuf",
			accept: "application/vnd.google.protobuf; proto=io.prometheus.client.MetricFamily; encoding=delimited",
		},
		{name: "gzip text", accept: "text/plain", compression: "gzip", contentType: "text/plain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
			req.Header.Set("Accept", test.accept)
			req.Header.Set("Accept-Encoding", test.compression)
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /metrics status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, test.contentType+";") {
				t.Fatalf("Content-Type = %q, want %s", got, test.contentType)
			}
			if got := rec.Header().Get("Content-Encoding"); got != test.compression {
				t.Fatalf("Content-Encoding = %q, want %q", got, test.compression)
			}
			vary := strings.Split(strings.Join(rec.Header().Values("Vary"), ","), ",")
			for _, want := range []string{"Accept", "Accept-Encoding"} {
				if !slices.ContainsFunc(vary, func(value string) bool {
					return strings.EqualFold(strings.TrimSpace(value), want)
				}) {
					t.Errorf("Vary = %q, want %s", vary, want)
				}
			}

			var body io.Reader = rec.Body
			if test.compression == "gzip" {
				reader, err := gzip.NewReader(body)
				if err != nil {
					t.Fatalf("open gzip response: %v", err)
				}
				defer reader.Close()
				body = reader
			}
			data, err := io.ReadAll(body)
			if err != nil {
				t.Fatalf("read metrics response: %v", err)
			}
			if len(data) == 0 ||
				(test.contentType == "text/plain" && !strings.Contains(string(data), "gobfd_negotiation_test 1\n")) {
				t.Fatalf("metrics response = %q, want the registered probe", data)
			}
		})
	}
}
