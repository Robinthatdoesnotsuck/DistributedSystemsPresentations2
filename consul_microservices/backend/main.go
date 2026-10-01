package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Item is the resource this service manages.
type Item struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Store is an in-memory "database" shared by every request goroutine.
// net/http spins up a new goroutine per incoming request, so without
// synchronization, concurrent reads/writes to the map below would race.
// sync.RWMutex fixes that: many readers can proceed together, but a writer
// gets exclusive access.
type Store struct {
	mu     sync.RWMutex
	items  map[int]Item
	nextID int
}

func NewStore() *Store {
	return &Store{items: make(map[int]Item), nextID: 1}
}

func (s *Store) Create(name string) Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := Item{ID: s.nextID, Name: name}
	s.items[item.ID] = item
	s.nextID++
	return item
}

func (s *Store) GetAll() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Item, 0, len(s.items))
	for _, it := range s.items {
		out = append(out, it)
	}
	return out
}

func (s *Store) Get(id int) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	it, ok := s.items[id]
	return it, ok
}

func (s *Store) Update(id int, name string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return Item{}, false
	}
	it.Name = name
	s.items[id] = it
	return it, true
}

func (s *Store) Delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return false
	}
	delete(s.items, id)
	return true
}

var store = NewStore()

func itemsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		json.NewEncoder(w).Encode(store.GetAll())

	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		item := store.Create(body.Name)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(item)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func itemHandler(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/items/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		item, ok := store.Get(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(item)

	case http.MethodPut:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		item, ok := store.Update(id, body.Name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(item)

	case http.MethodDelete:
		if !store.Delete(id) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("backend alive"))
}

// instanceID identifies which container answered a request. Set via the
// INSTANCE_ID env var in docker-compose.yml so you can watch a gateway's
// load balancer spread requests across backend-1/2/3.
var instanceID = "unknown"

// withInstanceHeader tags every response with X-Instance-Id so a client
// (or curl -i) can see which backend instance actually served it.
func withInstanceHeader(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Instance-Id", instanceID)
		next(w, r)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// registerWithConsul tells Consul "I exist, here's how to reach me, and
// here's how to check whether I'm healthy." Consul then takes over the
// actual health polling — nobody else (no gateway) needs to do it anymore.
// Retries a few times since docker-compose's depends_on only guarantees
// the consul container has *started*, not that its API is ready yet.
func registerWithConsul(consulAddr string) error {
	payload := map[string]any{
		"ID":      instanceID,
		"Name":    "backend",
		"Address": instanceID, // resolvable via Docker's embedded DNS
		"Port":    8080,
		"Check": map[string]any{
			"HTTP":                           fmt.Sprintf("http://%s:8080/heartbeat", instanceID),
			"Interval":                       "5s",
			"Timeout":                        "2s",
			"DeregisterCriticalServiceAfter": "1m",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 1; attempt <= 10; attempt++ {
		req, err := http.NewRequest(http.MethodPut, consulAddr+"/v1/agent/service/register", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				log.Printf("registered %s with Consul at %s", instanceID, consulAddr)
				return nil
			}
			lastErr = fmt.Errorf("consul returned status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	return fmt.Errorf("giving up registering with consul: %w", lastErr)
}

// deregisterFromConsul is called on shutdown so Consul (and therefore
// every gateway watching it) stops routing traffic here immediately,
// instead of waiting for DeregisterCriticalServiceAfter to time it out.
func deregisterFromConsul(consulAddr string) {
	req, err := http.NewRequest(http.MethodPut, consulAddr+"/v1/agent/service/deregister/"+instanceID, nil)
	if err != nil {
		log.Printf("failed to build deregister request: %v", err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("failed to deregister %s from consul: %v", instanceID, err)
		return
	}
	resp.Body.Close()
	log.Printf("deregistered %s from consul", instanceID)
}

func main() {
	if v := os.Getenv("INSTANCE_ID"); v != "" {
		instanceID = v
	}
	consulAddr := getEnv("CONSUL_HTTP_ADDR", "http://consul:8500")

	if err := registerWithConsul(consulAddr); err != nil {
		// Non-fatal: the service still runs and serves requests, it just
		// won't be discoverable via Consul until this succeeds. Logged
		// loudly because silently running "invisible" is a confusing
		// failure mode.
		log.Printf("WARNING: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/items", withInstanceHeader(itemsHandler))
	mux.HandleFunc("/items/", withInstanceHeader(itemHandler))
	mux.HandleFunc("/heartbeat", withInstanceHeader(heartbeatHandler))

	srv := &http.Server{Addr: ":8080", Handler: mux}

	// Graceful shutdown: on SIGTERM/SIGINT (what `docker stop` sends),
	// deregister from Consul BEFORE the process actually exits, so
	// gateways stop getting routed to a backend that's already gone.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("shutting down %s", instanceID)
		deregisterFromConsul(consulAddr)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("backend %s listening on :8080", instanceID)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
