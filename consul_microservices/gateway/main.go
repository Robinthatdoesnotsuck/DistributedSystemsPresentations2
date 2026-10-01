package main

import (
	"encoding/json"
	"fmt"
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

// RouteConfig is one entry from routes.json: a path prefix mapped to the
// NAME of a Consul service. This is now the entire static part of routing —
// no IPs, no ports, no target list. Consul is the single source of truth
// for which instances of that service actually exist and are healthy.
type RouteConfig struct {
	Prefix  string `json:"prefix"`
	Service string `json:"service"`
}

// Backend is one instance of a service, as currently reported by Consul.
type Backend struct {
	URL   *url.URL
	Proxy *httputil.ReverseProxy
}

// RouteGroup binds a prefix to a Consul service name, plus whatever
// healthy instances Consul most recently reported. The backend list is
// replaced wholesale under a mutex every time discovery refreshes it —
// no per-backend health flags to manage anymore, because Consul's
// `?passing=true` filter already excludes unhealthy instances for us.
type RouteGroup struct {
	Prefix  string
	Service string

	mu       sync.RWMutex
	backends []*Backend
	counter  uint64
}

// next returns the next backend in round-robin order across the pool
// Consul most recently reported as healthy for this service.
func (rg *RouteGroup) next() *Backend {
	rg.mu.RLock()
	defer rg.mu.RUnlock()
	n := len(rg.backends)
	if n == 0 {
		return nil
	}
	idx := atomic.AddUint64(&rg.counter, 1) % uint64(n)
	return rg.backends[idx]
}

func (rg *RouteGroup) setBackends(backends []*Backend) {
	rg.mu.Lock()
	rg.backends = backends
	rg.mu.Unlock()
}

func (rg *RouteGroup) snapshot() []string {
	rg.mu.RLock()
	defer rg.mu.RUnlock()
	out := make([]string, 0, len(rg.backends))
	for _, b := range rg.backends {
		out = append(out, b.URL.String())
	}
	return out
}

// Gateway holds every route group and a semaphore used as a bounded
// worker pool for in-flight proxied requests, same as before.
type Gateway struct {
	groups     []*RouteGroup
	sem        chan struct{}
	consulAddr string
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

func NewGateway(cfgs []RouteConfig, consulAddr string, maxConcurrent int) *Gateway {
	var groups []*RouteGroup
	for _, cfg := range cfgs {
		groups = append(groups, &RouteGroup{Prefix: cfg.Prefix, Service: cfg.Service})
	}
	return &Gateway{
		groups:     groups,
		sem:        make(chan struct{}, maxConcurrent),
		consulAddr: consulAddr,
	}
}

// ServeHTTP still layers a worker-pool semaphore on top of net/http's
// default per-request goroutine, same as every earlier version of this
// gateway. Only the backend *selection* logic changed.
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
		log.Printf("[%s] %s -> %s (service %q)", r.Method, r.URL.Path, backend.URL, rg.Service)
		backend.Proxy.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// consulServiceEntry mirrors the small slice of Consul's health API
// response this gateway actually needs.
type consulServiceEntry struct {
	Service struct {
		Service string `json:"Service"`
		Address string `json:"Address"`
		Port    int    `json:"Port"`
	} `json:"Service"`
}

// queryConsul asks Consul for every currently-passing (healthy) instance
// of a service. This one HTTP call replaces both the old static
// routes.json target list AND the gateway's own heartbeat-polling code —
// Consul is already doing that health checking, once, for everyone.
func queryConsul(consulAddr, service string) ([]*Backend, error) {
	reqURL := fmt.Sprintf("%s/v1/health/service/%s?passing=true", consulAddr, service)
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var entries []consulServiceEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, err
	}

	backends := make([]*Backend, 0, len(entries))
	for _, e := range entries {
		target, err := url.Parse(fmt.Sprintf("http://%s:%d", e.Service.Address, e.Service.Port))
		if err != nil {
			continue
		}
		backends = append(backends, &Backend{
			URL:   target,
			Proxy: httputil.NewSingleHostReverseProxy(target),
		})
	}
	return backends, nil
}

// refreshAll fans out one goroutine per route group to query Consul
// concurrently, joins with a WaitGroup, and swaps each group's backend
// list. Same fan-out/fan-in shape as the old heartbeat monitor — just
// asking Consul instead of polling each backend directly.
func (g *Gateway) refreshAll() {
	var wg sync.WaitGroup
	for _, rg := range g.groups {
		wg.Add(1)
		go func(rg *RouteGroup) {
			defer wg.Done()
			backends, err := queryConsul(g.consulAddr, rg.Service)
			if err != nil {
				log.Printf("consul query for service %q failed: %v", rg.Service, err)
				return
			}
			rg.setBackends(backends)
		}(rg)
	}
	wg.Wait()
}

func (g *Gateway) startDiscoveryLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			g.refreshAll()
		}
	}()
}

// statusHandler shows exactly what this gateway currently believes is
// healthy for each route, straight from its last Consul refresh.
func (g *Gateway) statusHandler(w http.ResponseWriter, r *http.Request) {
	out := make(map[string][]string)
	for _, rg := range g.groups {
		out[rg.Prefix] = rg.snapshot()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("gateway alive"))
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	cfgs, err := loadRouteConfigs("routes.json")
	if err != nil {
		log.Fatalf("failed to load routes.json: %v", err)
	}

	consulAddr := getEnv("CONSUL_HTTP_ADDR", "http://consul:8500")
	gw := NewGateway(cfgs, consulAddr, 10) // at most 10 requests proxied concurrently

	// Try a few times at startup: docker-compose's depends_on only
	// guarantees the consul container has started, not that backends have
	// registered with it yet.
	for attempt := 0; attempt < 5; attempt++ {
		gw.refreshAll()
		total := 0
		for _, rg := range gw.groups {
			total += len(rg.snapshot())
		}
		if total > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}

	gw.startDiscoveryLoop(5 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/heartbeat", heartbeatHandler)
	mux.HandleFunc("/status", gw.statusHandler)
	mux.Handle("/", gw)

	log.Printf("gateway listening on :8000, discovering backends via Consul at %s", consulAddr)
	log.Fatal(http.ListenAndServe(":8000", mux))
}
