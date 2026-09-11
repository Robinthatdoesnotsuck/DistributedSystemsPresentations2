package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RouteConfig is one entry from routes.json: a path prefix mapped to a pool
// of backend URLs. This is our "static service discovery" — no registry,
// just a file read at startup.
type RouteConfig struct {
	Prefix  string   `json:"prefix"`
	Targets []string `json:"targets"`
}

// Backend is one instance behind a route. healthy is an atomic bool so the
// heartbeat monitor (writer) and the request path (reader) can touch it
// concurrently without a mutex.
type Backend struct {
	URL     *url.URL
	Proxy   *httputil.ReverseProxy
	healthy atomic.Bool
}

// RouteGroup is a prefix plus its pool of backends and the state needed to
// round-robin across them.
type RouteGroup struct {
	Prefix   string
	Backends []*Backend
	counter  uint64 // atomic; next index = counter % len(Backends)
}

// next picks the next backend in round-robin order, skipping any instance
// currently marked unhealthy. Returns nil if every backend in the pool is
// down, so the caller can fail the request instead of proxying into a
// black hole.
func (rg *RouteGroup) next() *Backend {
	n := len(rg.Backends)
	for i := 0; i < n; i++ {
		idx := atomic.AddUint64(&rg.counter, 1) % uint64(n)
		b := rg.Backends[idx]
		if b.healthy.Load() {
			return b
		}
	}
	return nil
}

// Gateway holds all route groups and a semaphore used as a bounded
// worker pool for in-flight proxied requests.
type Gateway struct {
	groups []*RouteGroup
	sem    chan struct{}
}

func loadRouteConfigs(path string) ([]RouteConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfgs []RouteConfig
	if err := json.Unmarshal(data, &cfgs); err != nil {
		return nil, err
	}
	return cfgs, nil
}

func NewGateway(cfgs []RouteConfig, maxConcurrent int) *Gateway {
	var groups []*RouteGroup

	for _, cfg := range cfgs {
		rg := &RouteGroup{Prefix: cfg.Prefix}
		for _, t := range cfg.Targets {
			target, err := url.Parse(t)
			if err != nil {
				log.Fatalf("invalid target url %s: %v", t, err)
			}
			b := &Backend{
				URL:   target,
				Proxy: httputil.NewSingleHostReverseProxy(target),
			}
			b.healthy.Store(true) // assume healthy until the first heartbeat check says otherwise
			rg.Backends = append(rg.Backends, b)
		}
		groups = append(groups, rg)
	}

	return &Gateway{
		groups: groups,
		sem:    make(chan struct{}, maxConcurrent),
	}
}

// ServeHTTP is invoked by net/http in its own goroutine per request. We add
// two layers on top of that default concurrency:
//  1. A worker-pool semaphore capping how many requests are proxied at once.
//  2. Round-robin load balancing across the matching route's healthy backends.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.sem <- struct{}{}
	defer func() { <-g.sem }()

	for _, rg := range g.groups {
		if !strings.HasPrefix(r.URL.Path, rg.Prefix) {
			continue
		}
		backend := rg.next()
		if backend == nil {
			http.Error(w, "no healthy backend available", http.StatusServiceUnavailable)
			return
		}
		log.Printf("[%s] %s -> %s (prefix %q)", r.Method, r.URL.Path, backend.URL, rg.Prefix)
		backend.Proxy.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// startHeartbeatMonitor polls every backend's /heartbeat endpoint on a fixed
// interval. Each round fans out one goroutine per backend and joins them
// with a WaitGroup; each backend's own healthy flag is updated atomically,
// so no shared mutex is needed here.
func (g *Gateway) startHeartbeatMonitor(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			var wg sync.WaitGroup
			for _, rg := range g.groups {
				for _, b := range rg.Backends {
					wg.Add(1)
					go func(b *Backend) {
						defer wg.Done()
						b.healthy.Store(checkHeartbeat(b.URL.String()))
					}(b)
				}
			}
			wg.Wait()
		}
	}()
}

func checkHeartbeat(target string) bool {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(target + "/heartbeat")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// statusHandler reports every backend's last-known health, grouped by
// route prefix, so you can watch load balancing / failover happen live.
func (g *Gateway) statusHandler(w http.ResponseWriter, r *http.Request) {
	type backendStatus struct {
		Target  string `json:"target"`
		Healthy bool   `json:"healthy"`
	}
	out := make(map[string][]backendStatus)
	for _, rg := range g.groups {
		var list []backendStatus
		for _, b := range rg.Backends {
			list = append(list, backendStatus{Target: b.URL.String(), Healthy: b.healthy.Load()})
		}
		out[rg.Prefix] = list
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("gateway alive"))
}

func main() {
	cfgs, err := loadRouteConfigs("routes.json")
	if err != nil {
		log.Fatalf("failed to load routes.json: %v", err)
	}

	gw := NewGateway(cfgs, 10) // at most 10 requests proxied concurrently
	gw.startHeartbeatMonitor(5 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/heartbeat", heartbeatHandler)
	mux.HandleFunc("/status", gw.statusHandler)
	mux.Handle("/", gw)

	log.Println("gateway listening on :8000")
	log.Fatal(http.ListenAndServe(":8000", mux))
}
