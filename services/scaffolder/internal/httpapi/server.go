// Package httpapi exposes the scaffolder service over HTTP.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/discovery"
	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/provision"
)

// Discoverer is the part of the discovery package this server depends on.
type Discoverer interface {
	Discover(ctx context.Context) (discovery.Result, error)
}

// Scaffolder is the part of the provision package this server depends on.
type Scaffolder interface {
	Scaffold(ctx context.Context, req provision.Request) (provision.Result, error)
}

// Server wires the HTTP handlers.
type Server struct {
	discoverer Discoverer
	scaffolder Scaffolder
	owner      string
	logger     *slog.Logger
	registry   *prometheus.Registry

	discoveries    *prometheus.CounterVec
	discoveredRepo prometheus.Gauge
	scaffolds      *prometheus.CounterVec
	duration       *prometheus.HistogramVec
}

// New builds a Server with its own metrics registry.
func New(d Discoverer, sc Scaffolder, owner string, logger *slog.Logger) *Server {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)
	return &Server{
		discoverer: d,
		scaffolder: sc,
		owner:      owner,
		logger:     logger,
		registry:   reg,
		discoveries: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "scaffolder_discovery_requests_total",
			Help: "Catalog discovery requests, by outcome.",
		}, []string{"outcome"}),
		discoveredRepo: factory.NewGauge(prometheus.GaugeOpts{
			Name: "scaffolder_discovery_targets",
			Help: "Number of catalog targets returned by the last discovery.",
		}),
		scaffolds: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "scaffolder_scaffold_requests_total",
			Help: "Scaffold requests, by outcome.",
		}, []string{"outcome"}),
		duration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "scaffolder_http_request_duration_seconds",
			Help:    "HTTP request duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
	}
}

// Handler returns the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleHealth)
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{Registry: s.registry}))
	mux.HandleFunc("GET /catalog/discovery", s.timed("catalog_discovery", s.handleDiscovery))
	mux.HandleFunc("POST /scaffold", s.timed("scaffold", s.handleScaffold))
	return mux
}

func (s *Server) timed(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next(w, r)
		s.duration.WithLabelValues(route).Observe(time.Since(start).Seconds())
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleDiscovery serves a Backstage Location entity listing every repository of
// the owner that carries a catalog file. Backstage consumes it as a plain url
// location; all the GitHub logic stays on this side, in Go.
func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	result, err := s.discoverer.Discover(r.Context())
	if err != nil {
		s.discoveries.WithLabelValues("error").Inc()
		s.logger.Error("discovery failed", "error", err)
		writeJSON(w, http.StatusBadGateway, errorBody{Error: "discovery failed"})
		return
	}
	s.discoveries.WithLabelValues("success").Inc()
	s.discoveredRepo.Set(float64(len(result.Targets)))

	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write([]byte(result.LocationEntity(s.owner)))
}

// errorBody is the shape of every error response, so callers can rely on one
// field name across the endpoints.
type errorBody struct {
	Error      string                 `json:"error"`
	Problems   []string               `json:"problems,omitempty"`
	FailedStep string                 `json:"failedStep,omitempty"`
	Repository *provision.RepoOutcome `json:"repository,omitempty"`
	Detail     string                 `json:"detail,omitempty"`
}

// handleScaffold creates the repository and applies the custom resource.
//
// The status codes carry the distinction that matters to a caller:
//
//	400  the request was rejected before anything was created
//	201  everything was created
//	207  the repository exists but the custom resource does not, and the same
//	     request can be retried to finish the job
//	502  nothing usable was created
func (s *Server) handleScaffold(w http.ResponseWriter, r *http.Request) {
	var req provision.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.scaffolds.WithLabelValues("bad_request").Inc()
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "malformed JSON body: " + err.Error()})
		return
	}

	result, err := s.scaffolder.Scaffold(r.Context(), req)

	var invalid *provision.ValidationError
	if errors.As(err, &invalid) {
		s.scaffolds.WithLabelValues("invalid").Inc()
		writeJSON(w, http.StatusBadRequest, errorBody{
			Error:    "invalid request",
			Problems: invalid.Problems,
		})
		return
	}

	var partial *provision.PartialError
	if errors.As(err, &partial) {
		s.scaffolds.WithLabelValues("partial").Inc()
		s.logger.Error("provisioning did not finish", "repo", partial.Repository.URL, "step", partial.Step, "error", partial.Err)
		writeJSON(w, http.StatusMultiStatus, errorBody{
			Error:      partial.Error(),
			FailedStep: partial.Step,
			Repository: &partial.Repository,
			Detail: "the repository was created and left in place, tagged with the " +
				provision.TopicIncomplete + " topic. Nothing is deleted automatically. " +
				"Re-send the same request to finish: every step is idempotent.",
		})
		return
	}

	if err != nil {
		s.scaffolds.WithLabelValues("error").Inc()
		s.logger.Error("scaffold failed", "error", err)
		writeJSON(w, http.StatusBadGateway, errorBody{Error: err.Error()})
		return
	}

	s.scaffolds.WithLabelValues("success").Inc()
	writeJSON(w, http.StatusCreated, result)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
