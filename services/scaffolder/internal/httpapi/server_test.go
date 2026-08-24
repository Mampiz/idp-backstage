package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/discovery"
)

type stubDiscoverer struct {
	result discovery.Result
	err    error
}

func (s stubDiscoverer) Discover(context.Context) (discovery.Result, error) {
	return s.result, s.err
}

func newTestServer(d Discoverer) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(d, "mampiz", logger).Handler()
}

func TestDiscoveryEndpointServesALocationEntity(t *testing.T) {
	h := newTestServer(stubDiscoverer{result: discovery.Result{
		Targets:   []string{"https://github.com/mampiz/a/blob/main/catalog-info.yaml"},
		Scanned:   1,
		FetchedAt: time.Now(),
	}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/catalog/discovery", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("content-type = %q, want application/yaml", ct)
	}
	if !strings.Contains(rec.Body.String(), "kind: Location") {
		t.Errorf("body is not a Location entity:\n%s", rec.Body.String())
	}
}

func TestDiscoveryEndpointReportsUpstreamFailures(t *testing.T) {
	h := newTestServer(stubDiscoverer{err: errors.New("github is down")})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/catalog/discovery", nil))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestHealthEndpoints(t *testing.T) {
	h := newTestServer(stubDiscoverer{})
	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, rec.Code)
		}
	}
}

func TestMetricsEndpointExposesDiscoveryCounters(t *testing.T) {
	h := newTestServer(stubDiscoverer{result: discovery.Result{FetchedAt: time.Now()}})
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/catalog/discovery", nil))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "scaffolder_discovery_requests_total") {
		t.Errorf("metrics do not include the discovery counter:\n%s", rec.Body.String())
	}
}
