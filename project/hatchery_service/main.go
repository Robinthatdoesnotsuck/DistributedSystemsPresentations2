// hatchery_service manages Clusters: it registers workers with larva_service,
// dispatches jobs to them, and records status reports as they come back.
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

const larvaAddr = "http://localhost:8082"

type clusterStore struct {
	mu       sync.Mutex
	clusters map[string]*pb.Cluster
}

var store = &clusterStore{clusters: make(map[string]*pb.Cluster)}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/clusters", handleGetOrCreateCluster)
	mux.HandleFunc("/clusters/workers", handleRegisterWorker)
	mux.HandleFunc("/clusters/dispatch", handleDispatchJob)
	mux.HandleFunc("/jobs/report", handleJobReport)

	log.Println("hatchery_service listening on :8081")
	if err := http.ListenAndServe(":8081", mux); err != nil {
		log.Fatal(err)
	}
}

func handleGetOrCreateCluster(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		store.mu.Lock()
		cluster, ok := store.clusters[id]
		store.mu.Unlock()
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		writeProto(w, cluster)

	case http.MethodPost:
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
		cluster := &pb.Cluster{
			Id:     fmt.Sprintf("cluster-%d", rand.Intn(1_000_000)),
			Status: "active",
		}
		store.mu.Lock()
		store.clusters[cluster.Id] = cluster
		store.mu.Unlock()
		log.Printf("created %s for account %d", cluster.Id, req.AccountId)
		writeProto(w, cluster)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRegisterWorker forwards a worker-registration request to
// larva_service, then tracks the returned worker under its cluster.
func handleRegisterWorker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req pb.RegisterWorkerRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	store.mu.Lock()
	cluster, ok := store.clusters[req.ClusterId]
	store.mu.Unlock()
	if !ok {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return
	}

	worker, err := callLarva(larvaAddr+"/workers", &req, &pb.Worker{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	store.mu.Lock()
	cluster.Workers = append(cluster.Workers, worker.(*pb.Worker))
	store.mu.Unlock()

	writeProto(w, worker)
}

// handleDispatchJob creates a ClusterJob record and forwards the underlying
// WorkerJob to the target worker on larva_service.
func handleDispatchJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req pb.AssignJobRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp, err := callLarva(larvaAddr+"/workers/assign", &req, &pb.AssignJobResponse{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("dispatched job %q to %s", req.Job.Task, req.WorkerId)
	writeProto(w, resp.(*pb.AssignJobResponse))
}

// handleJobReport is called by larva_service workers to report completion.
func handleJobReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req pb.ReportJobStatusRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("worker %s: job %q -> %s", req.WorkerId, req.Task, req.Status)
	writeProto(w, &pb.ReportJobStatusResponse{Acknowledged: true})
}

func callLarva(url string, reqMsg, respMsg proto.Message) (proto.Message, error) {
	body, err := proto.Marshal(reqMsg)
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(url, "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if err := proto.Unmarshal(respBody, respMsg); err != nil {
		return nil, err
	}
	return respMsg, nil
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
