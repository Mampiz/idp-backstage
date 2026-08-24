package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mampiz/idp-backstage/services/status-api/internal/webapps"
)

type stubStore struct {
	items  []webapps.WebApp
	synced bool
}

func (s stubStore) List() []webapps.WebApp { return s.items }

func (s stubStore) Get(namespace, name string) (webapps.WebApp, bool) {
	for _, app := range s.items {
		if app.Namespace == namespace && app.Name == name {
			return app, true
		}
	}
	return webapps.WebApp{}, false
}

func (s stubStore) HasSynced() bool { return s.synced }

func newHandler(store Store) http.Handler {
	return New(store, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
}

func sampleApps() []webapps.WebApp {
	return []webapps.WebApp{
		{
			Name: "alpha", Namespace: "idp-demo", Available: true,
			Replicas: webapps.Replicas{Desired: 2, Effective: 2, Ready: 2},
			Image:    webapps.Image{Desired: "nginx:1.27-alpine", Deployed: "nginx:1.27-alpine"},
			Condition: &webapps.Condition{
				Status: "True", Reason: "DeploymentReady", Message: "all replicas are ready",
			},
		},
		{Name: "beta", Namespace: "other", Available: false},
	}
}

func TestListReturnsEveryWebApp(t *testing.T) {
	h := newHandler(stubStore{items: sampleApps(), synced: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/webapps", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body webapps.List
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if body.Count != 2 || len(body.Items) != 2 {
		t.Errorf("count = %d, items = %d, want 2 and 2", body.Count, len(body.Items))
	}
}

func TestListFiltersByNamespace(t *testing.T) {
	h := newHandler(stubStore{items: sampleApps(), synced: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/webapps?namespace=idp-demo", nil))

	var body webapps.List
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if body.Count != 1 || body.Items[0].Name != "alpha" {
		t.Errorf("filtered result = %+v", body.Items)
	}
}

func TestGetReturnsOneWebAppWithItsCondition(t *testing.T) {
	h := newHandler(stubStore{items: sampleApps(), synced: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/webapps/idp-demo/alpha", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var app webapps.WebApp
	if err := json.Unmarshal(rec.Body.Bytes(), &app); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if app.Condition == nil || app.Condition.Status != "True" {
		t.Errorf("condition = %+v", app.Condition)
	}
	if app.Replicas.Ready != 2 || app.Replicas.Desired != 2 {
		t.Errorf("replicas = %+v", app.Replicas)
	}
}

func TestGetReturnsNotFound(t *testing.T) {
	h := newHandler(stubStore{items: sampleApps(), synced: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/webapps/idp-demo/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// A cold cache must not be reported as "no WebApps".
func TestReadyzFailsUntilTheCachesAreSynced(t *testing.T) {
	h := newHandler(stubStore{synced: false})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("readyz status = %d, want 503 while syncing", rec.Code)
	}

	// Liveness is a different question and must still answer.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", rec.Code)
	}
}

func TestReadyzSucceedsOnceSynced(t *testing.T) {
	h := newHandler(stubStore{synced: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("readyz status = %d, want 200", rec.Code)
	}
}

func TestMetricsReportTheCacheContents(t *testing.T) {
	h := newHandler(stubStore{items: sampleApps(), synced: true})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`status_api_webapps{available="true"} 1`,
		`status_api_webapps{available="false"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics are missing %q", want)
		}
	}
}
