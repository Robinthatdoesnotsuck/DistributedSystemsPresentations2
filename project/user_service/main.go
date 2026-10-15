// user_service owns Accounts. Creating a cluster for an account forwards
// the request on to hatchery_service and stores the resulting cluster id.
package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sync"

	pb "orchestrator/proto_gen"

	"google.golang.org/protobuf/proto"
)

const hatcheryAddr = "http://localhost:8081"

type accountStore struct {
	mu       sync.Mutex
	accounts map[int32]*pb.Account
}

var store = &accountStore{accounts: make(map[int32]*pb.Account)}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", handleGetOrCreateAccount)
	mux.HandleFunc("/accounts/clusters", handleCreateClusterForAccount)

	log.Println("user_service listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}

func handleGetOrCreateAccount(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var id int32
		fmt.Sscanf(r.URL.Query().Get("id"), "%d", &id)
		store.mu.Lock()
		account, ok := store.accounts[id]
		store.mu.Unlock()
		if !ok {
			http.Error(w, "account not found", http.StatusNotFound)
			return
		}
		writeProto(w, account)

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req pb.CreateAccountRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		account := &pb.Account{
			Id:       rand.Int31n(1_000_000),
			UserName: req.UserName,
		}
		store.mu.Lock()
		store.accounts[account.Id] = account
		store.mu.Unlock()
		log.Printf("created account %d (%s)", account.Id, account.UserName)
		writeProto(w, account)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleCreateClusterForAccount forwards a CreateClusterRequest to
// hatchery_service, and appends the resulting Cluster to the account.
func handleCreateClusterForAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req pb.CreateClusterRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	store.mu.Lock()
	account, ok := store.accounts[req.AccountId]
	store.mu.Unlock()
	if !ok {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	reqBody, err := proto.Marshal(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := http.Post(hatcheryAddr+"/clusters", "application/x-protobuf", bytes.NewReader(reqBody))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var cluster pb.Cluster
	if err := proto.Unmarshal(respBody, &cluster); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	store.mu.Lock()
	account.Clusters = append(account.Clusters, &cluster)
	store.mu.Unlock()

	log.Printf("account %d now owns %s", account.Id, cluster.Id)
	writeProto(w, &cluster)
}

func writeProto(w http.ResponseWriter, msg proto.Message) {
	body, err := proto.Marshal(msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(body)
}
