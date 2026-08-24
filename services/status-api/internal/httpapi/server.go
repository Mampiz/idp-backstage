// Package httpapi exposes the WebApp status over HTTP.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Mampiz/idp-backstage/services/status-api/internal/webapps"
)

// Store is the part of the webapps package this server depends on.
type Store interface {
	List() []webapps.WebApp
	Get(namespace, name string) (webapps.WebApp, bool)
	HasSynced() bool
}

// Server wires the HTTP handlers.
type Server struct {
	store    Store
	logger   *slog.Logger
	registry *prometheus.Registry

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	tracked  *prometheus.GaugeVec
}

// New builds a Server with its own metrics registry.
func New(store Store, logger *slog.Logger) *Server {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)
	return &Server{
		store:    store,
		logger:   logger,
		registry: reg,
		requests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "status_api_requests_total",
			Help: "HTTP requests, by route and status code.",
		}, []string{"route", "code"}),
		duration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "status_api_request_duration_seconds",
			Help:    "HTTP request duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		tracked: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "status_api_webapps",
			Help: "WebApp custom resources in the cache, by availability.",
		}, []string{"available"}),
	}
}

// Handler returns the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleLive)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.Handle("GET /metrics", s.collectThen(promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{Registry: s.registry})))
	mux.HandleFunc("GET /api/webapps", s.instrument("webapps_list", s.handleList))
	mux.HandleFunc("GET /api/webapps/{namespace}/{name}", s.instrument("webapps_get", s.handleGet))
	return mux
}

// statusRecorder captures the status code so it can be used as a metric label.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) instrument(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next(rec, r)
		s.duration.WithLabelValues(route).Observe(time.Since(start).Seconds())
		s.requests.WithLabelValues(route, http.StatusText(rec.code)).Inc()
	}
}

// collectThen refreshes the cache gauges right before the metrics are scraped,
// so the numbers reflect the cache rather than the last request that happened
// to touch it.
func (s *Server) collectThen(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var available, unavailable float64
		for _, app := range s.store.List() {
			if app.Available {
				available++
			} else {
				unavailable++
			}
		}
		s.tracked.WithLabelValues("true").Set(available)
		s.tracked.WithLabelValues("false").Set(unavailable)
		next.ServeHTTP(w, r)
	})
}

// statusBody is the shape of the probe responses.
type statusBody struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// handleReady reports not-ready until the informer caches are populated.
// Serving an empty list from a cold cache would read as "there are no WebApps",
// which is a different and much more misleading answer than "not ready yet".
func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	if !s.store.HasSynced() {
		writeJSON(w, http.StatusServiceUnavailable, statusBody{
			Status: "syncing",
			Detail: "informer caches are not populated yet",
		})
		return
	}
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	items := s.store.List()
	if namespace := r.URL.Query().Get("namespace"); namespace != "" {
		filtered := make([]webapps.WebApp, 0, len(items))
		for _, app := range items {
			if app.Namespace == namespace {
				filtered = append(filtered, app)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, webapps.List{Items: items, Count: len(items)})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")

	app, found := s.store.Get(namespace, name)
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no WebApp " + namespace + "/" + name,
		})
		return
	}
	writeJSON(w, http.StatusOK, app)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
