package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/discovery"
	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/provision"
)

type stubDiscoverer struct {
	result discovery.Result
	err    error
}

func (s stubDiscoverer) Discover(context.Context) (discovery.Result, error) {
	return s.result, s.err
}

type stubScaffolder struct {
	result provision.Result
	err    error
	got    provision.Request
}

func (s *stubScaffolder) Scaffold(_ context.Context, req provision.Request) (provision.Result, error) {
	s.got = req
	return s.result, s.err
}

func newTestServer(d Discoverer) http.Handler {
	return newTestServerWith(d, &stubScaffolder{})
}

func newTestServerWith(d Discoverer, sc Scaffolder) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(d, sc, "mampiz", logger).Handler()
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

func postScaffold(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/scaffold", strings.NewReader(body))
	h.ServeHTTP(rec, req)
	return rec
}

func TestScaffoldReturnsCreatedOnSuccess(t *testing.T) {
	stub := &stubScaffolder{result: provision.Result{
		Name:       "my-api",
		Repository: provision.RepoOutcome{URL: "https://github.com/Mampiz/my-api", Created: true},
		WebApp:     provision.WebAppOutcome{Namespace: "idp-apps", Name: "my-api", Applied: true},
	}}
	h := newTestServerWith(stubDiscoverer{}, stub)

	rec := postScaffold(h, `{"name":"my-api","image":"ghcr.io/mampiz/my-api:0.1.0","port":8080,"replicas":2}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if stub.got.Name != "my-api" || stub.got.Port != 8080 {
		t.Errorf("the request was not decoded properly: %+v", stub.got)
	}
}

// A rejected request must be a 400 that lists every problem at once, so a form
// does not have to be resubmitted once per mistake.
func TestScaffoldReturnsEveryValidationProblem(t *testing.T) {
	stub := &stubScaffolder{err: &provision.ValidationError{
		Problems: []string{"name is required", "image is required"},
	}}
	h := newTestServerWith(stubDiscoverer{}, stub)

	rec := postScaffold(h, `{}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body struct {
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(body.Problems) != 2 {
		t.Errorf("problems = %v, want both of them", body.Problems)
	}
}

// A half-finished run is neither a success nor a plain failure, and the response
// has to say which step failed and that the repository was left in place.
func TestScaffoldReportsAPartialRun(t *testing.T) {
	stub := &stubScaffolder{err: &provision.PartialError{
		Repository: provision.RepoOutcome{URL: "https://github.com/Mampiz/my-api", Created: true},
		Step:       "webapp",
		Err:        errors.New("admission webhook rejected the request"),
	}}
	h := newTestServerWith(stubDiscoverer{}, stub)

	rec := postScaffold(h, `{"name":"my-api","image":"x:1","port":80,"replicas":1}`)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "webapp") {
		t.Errorf("the response does not name the failed step: %s", body)
	}
	if !strings.Contains(body, provision.TopicIncomplete) {
		t.Errorf("the response does not mention the marker topic: %s", body)
	}
}

func TestScaffoldRejectsMalformedJSON(t *testing.T) {
	h := newTestServerWith(stubDiscoverer{}, &stubScaffolder{})

	rec := postScaffold(h, `{not json`)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
