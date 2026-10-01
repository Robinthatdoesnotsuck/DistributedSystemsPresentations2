package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
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
// INSTANCE_ID env var in docker-compose.yml so you can watch the gateway's
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

func main() {
	if v := os.Getenv("INSTANCE_ID"); v != "" {
		instanceID = v
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/items", withInstanceHeader(itemsHandler))
	mux.HandleFunc("/items/", withInstanceHeader(itemHandler))
	mux.HandleFunc("/heartbeat", withInstanceHeader(heartbeatHandler))

	log.Printf("backend %s listening on :8080", instanceID)
	log.Fatal(http.ListenAndServe(":8080", mux))
}
