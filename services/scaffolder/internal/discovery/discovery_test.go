package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v77/github"
)

// fakeGitHub is a minimal stand-in for the bits of the GitHub REST API this
// package uses, so the tests never touch the network.
type fakeGitHub struct {
	ownerType string
	repos     []map[string]any
	// withCatalog is the set of repository names that contain the catalog file.
	withCatalog map[string]bool
	calls       atomic.Int64
	failRepos   bool
}

func (f *fakeGitHub) server(t *testing.T) *github.Client {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		// /users/{owner} vs /users/{owner}/repos
		if len(parts) == 2 {
			writeJSON(w, map[string]any{"login": parts[1], "type": f.ownerType})
			return
		}
		if f.failRepos {
			http.Error(w, "upstream on fire", http.StatusInternalServerError)
			return
		}
		writeJSON(w, f.repos)
	})

	mux.HandleFunc("/orgs/", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		writeJSON(w, f.repos)
	})

	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		// /repos/{owner}/{repo}/contents/{path}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 5 {
			http.NotFound(w, r)
			return
		}
		repo := parts[2]
		if !f.withCatalog[repo] {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"type": "file", "name": "catalog-info.yaml"})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := github.NewClient(srv.Client())
	base, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parsing test server url: %v", err)
	}
	client.BaseURL = base
	return client
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func repo(name, branch string, archived bool) map[string]any {
	return map[string]any{"name": name, "default_branch": branch, "archived": archived}
}

func TestDiscoverKeepsOnlyRepositoriesWithACatalogFile(t *testing.T) {
	fake := &fakeGitHub{
		ownerType: "User",
		repos: []map[string]any{
			repo("has-catalog", "main", false),
			repo("no-catalog", "main", false),
			repo("also-has-catalog", "develop", false),
		},
		withCatalog: map[string]bool{"has-catalog": true, "also-has-catalog": true},
	}
	d := New(fake.server(t), "mampiz", "catalog-info.yaml", time.Minute)

	got, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []string{
		"https://github.com/mampiz/also-has-catalog/blob/develop/catalog-info.yaml",
		"https://github.com/mampiz/has-catalog/blob/main/catalog-info.yaml",
	}
	if fmt.Sprint(got.Targets) != fmt.Sprint(want) {
		t.Errorf("targets = %v, want %v", got.Targets, want)
	}
	if got.Scanned != 3 {
		t.Errorf("scanned = %d, want 3", got.Scanned)
	}
}

func TestDiscoverSkipsArchivedRepositories(t *testing.T) {
	fake := &fakeGitHub{
		ownerType:   "User",
		repos:       []map[string]any{repo("retired", "main", true)},
		withCatalog: map[string]bool{"retired": true},
	}
	d := New(fake.server(t), "mampiz", "catalog-info.yaml", time.Minute)

	got, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got.Targets) != 0 {
		t.Errorf("archived repository was discovered: %v", got.Targets)
	}
}

func TestDiscoverServesFromCacheWithinTTL(t *testing.T) {
	fake := &fakeGitHub{
		ownerType:   "User",
		repos:       []map[string]any{repo("has-catalog", "main", false)},
		withCatalog: map[string]bool{"has-catalog": true},
	}
	now := time.Now()
	d := New(fake.server(t), "mampiz", "catalog-info.yaml", time.Minute, WithClock(func() time.Time { return now }))

	if _, err := d.Discover(context.Background()); err != nil {
		t.Fatalf("first Discover: %v", err)
	}
	after := fake.calls.Load()
	if _, err := d.Discover(context.Background()); err != nil {
		t.Fatalf("second Discover: %v", err)
	}
	if fake.calls.Load() != after {
		t.Errorf("second call hit GitHub: %d calls before, %d after", after, fake.calls.Load())
	}
}

func TestDiscoverRefreshesAfterTTL(t *testing.T) {
	fake := &fakeGitHub{
		ownerType:   "User",
		repos:       []map[string]any{repo("has-catalog", "main", false)},
		withCatalog: map[string]bool{"has-catalog": true},
	}
	now := time.Now()
	d := New(fake.server(t), "mampiz", "catalog-info.yaml", time.Minute, WithClock(func() time.Time { return now }))

	if _, err := d.Discover(context.Background()); err != nil {
		t.Fatalf("first Discover: %v", err)
	}
	before := fake.calls.Load()
	now = now.Add(2 * time.Minute)
	if _, err := d.Discover(context.Background()); err != nil {
		t.Fatalf("second Discover: %v", err)
	}
	if fake.calls.Load() == before {
		t.Error("expired cache was not refreshed")
	}
}

// A GitHub outage must not empty the catalog: an empty target list would orphan
// every entity Backstage ingested from it.
func TestDiscoverServesStaleResultWhenGitHubFails(t *testing.T) {
	fake := &fakeGitHub{
		ownerType:   "User",
		repos:       []map[string]any{repo("has-catalog", "main", false)},
		withCatalog: map[string]bool{"has-catalog": true},
	}
	now := time.Now()
	d := New(fake.server(t), "mampiz", "catalog-info.yaml", time.Minute, WithClock(func() time.Time { return now }))

	first, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("first Discover: %v", err)
	}

	fake.failRepos = true
	now = now.Add(2 * time.Minute)

	second, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover returned an error instead of stale data: %v", err)
	}
	if fmt.Sprint(second.Targets) != fmt.Sprint(first.Targets) {
		t.Errorf("stale targets = %v, want %v", second.Targets, first.Targets)
	}
}

func TestDiscoverFailsWhenGitHubFailsAndNothingIsCached(t *testing.T) {
	fake := &fakeGitHub{ownerType: "User", failRepos: true}
	d := New(fake.server(t), "mampiz", "catalog-info.yaml", time.Minute)

	if _, err := d.Discover(context.Background()); err == nil {
		t.Fatal("expected an error when GitHub fails on a cold cache")
	}
}

func TestDiscoverUsesTheOrganizationEndpointForOrganizations(t *testing.T) {
	fake := &fakeGitHub{
		ownerType:   "Organization",
		repos:       []map[string]any{repo("has-catalog", "main", false)},
		withCatalog: map[string]bool{"has-catalog": true},
	}
	d := New(fake.server(t), "some-org", "catalog-info.yaml", time.Minute)

	got, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got.Targets) != 1 {
		t.Errorf("targets = %v, want exactly one", got.Targets)
	}
}

func TestLocationEntityRendersTargets(t *testing.T) {
	r := Result{
		Targets:   []string{"https://github.com/mampiz/a/blob/main/catalog-info.yaml"},
		Scanned:   3,
		FetchedAt: time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC),
	}
	out := r.LocationEntity("mampiz")

	for _, want := range []string{
		"kind: Location",
		"name: github-discovery",
		"type: url",
		"    - https://github.com/mampiz/a/blob/main/catalog-info.yaml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered entity is missing %q:\n%s", want, out)
		}
	}
}

// An empty list still has to be valid YAML, or Backstage rejects the location.
func TestLocationEntityRendersAnEmptyTargetList(t *testing.T) {
	out := Result{FetchedAt: time.Now()}.LocationEntity("mampiz")
	if !strings.Contains(out, "targets: []") {
		t.Errorf("empty result did not render an empty list:\n%s", out)
	}
}
