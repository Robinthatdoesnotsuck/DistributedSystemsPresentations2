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
	"time"
)

// Route is one entry from routes.json: a path prefix mapped to a backend URL.
// This is our "static service discovery" — no registry, just a file read at
// startup.
type Route struct {
	Prefix string `json:"prefix"`
	Target string `json:"target"`
}

// Gateway holds the routing table, one reverse proxy per route, a health
// map for the heartbeat monitor, and a semaphore channel used as a
// bounded worker pool for in-flight proxied requests.
type Gateway struct {
	routes  []Route
	proxies map[string]*httputil.ReverseProxy

	healthMu sync.RWMutex
	health   map[string]bool

	sem chan struct{} // acts as a worker-pool: buffered channel = pool size
}

func loadRoutes(path string) ([]Route, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var routes []Route
	if err := json.Unmarshal(data, &routes); err != nil {
		return nil, err
	}
	return routes, nil
}

func NewGateway(routes []Route, maxConcurrent int) *Gateway {
	proxies := make(map[string]*httputil.ReverseProxy)
	health := make(map[string]bool)

	for _, r := range routes {
		target, err := url.Parse(r.Target)
		if err != nil {
			log.Fatalf("invalid target url %s: %v", r.Target, err)
		}
		proxies[r.Prefix] = httputil.NewSingleHostReverseProxy(target)
		health[r.Target] = false
	}

	return &Gateway{
		routes:  routes,
		proxies: proxies,
		health:  health,
		sem:     make(chan struct{}, maxConcurrent),
	}
}

// ServeHTTP is invoked by net/http in its own goroutine for every incoming
// request (that's Go's default request-level concurrency). On top of that
// we add an explicit worker-pool pattern: each request must first acquire a
// slot from the semaphore channel before being proxied downstream. This
// caps how many requests are simultaneously forwarded to backends, which is
// a common way to protect a downstream service from being overwhelmed.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.sem <- struct{}{}        // acquire a worker slot (blocks if pool is full)
	defer func() { <-g.sem }() // release the slot when done

	for prefix, proxy := range g.proxies {
		if strings.HasPrefix(r.URL.Path, prefix) {
			log.Printf("[%s] %s -> matched prefix %q", r.Method, r.URL.Path, prefix)
			proxy.ServeHTTP(w, r)
			return
		}
	}
	http.NotFound(w, r)
}

// startHeartbeatMonitor polls every backend's /heartbeat endpoint on a fixed
// interval. Each poll round fans out one goroutine per backend, joins them
// with a WaitGroup, and writes results behind healthMu — a small,
// self-contained demonstration of fan-out/fan-in plus mutex-protected
// shared state.
func (g *Gateway) startHeartbeatMonitor(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for range ticker.C {
			var wg sync.WaitGroup
			for _, r := range g.routes {
				wg.Add(1)
				go func(target string) {
					defer wg.Done()
					ok := checkHeartbeat(target)
					g.healthMu.Lock()
					g.health[target] = ok
					g.healthMu.Unlock()
				}(r.Target)
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

func (g *Gateway) statusHandler(w http.ResponseWriter, r *http.Request) {
	g.healthMu.RLock()
	defer g.healthMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(g.health)
}

func heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("gateway alive"))
}

func main() {
	routes, err := loadRoutes("routes.json")
	if err != nil {
		log.Fatalf("failed to load routes.json: %v", err)
	}

	gw := NewGateway(routes, 10) // at most 10 requests proxied concurrently
	gw.startHeartbeatMonitor(5 * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/heartbeat", heartbeatHandler) // is the gateway itself alive
	mux.HandleFunc("/status", gw.statusHandler)     // what does the gateway see for backends
	mux.Handle("/", gw)                             // everything else: route by routes.json

	log.Println("gateway listening on :8000")
	log.Fatal(http.ListenAndServe(":8000", mux))
}
