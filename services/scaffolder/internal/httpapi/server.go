// Package httpapi exposes the scaffolder service over HTTP.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Mampiz/idp-backstage/services/scaffolder/internal/discovery"
)

// Discoverer is the part of the discovery package this server depends on.
type Discoverer interface {
	Discover(ctx context.Context) (discovery.Result, error)
}

// Server wires the HTTP handlers.
type Server struct {
	discoverer Discoverer
	owner      string
	logger     *slog.Logger
	registry   *prometheus.Registry

	discoveries    *prometheus.CounterVec
	discoveredRepo prometheus.Gauge
	duration       *prometheus.HistogramVec
}

// New builds a Server with its own metrics registry.
func New(d Discoverer, owner string, logger *slog.Logger) *Server {
	reg := prometheus.NewRegistry()
	factory := promauto.With(reg)
	return &Server{
		discoverer: d,
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleDiscovery serves a Backstage Location entity listing every repository of
// the owner that carries a catalog file. Backstage consumes it as a plain url
// location; all the GitHub logic stays on this side, in Go.
func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	result, err := s.discoverer.Discover(r.Context())
	if err != nil {
		s.discoveries.WithLabelValues("error").Inc()
		s.logger.Error("discovery failed", "error", err)
		http.Error(w, "discovery failed", http.StatusBadGateway)
		return
	}
	s.discoveries.WithLabelValues("success").Inc()
	s.discoveredRepo.Set(float64(len(result.Targets)))

	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write([]byte(result.LocationEntity(s.owner)))
}
