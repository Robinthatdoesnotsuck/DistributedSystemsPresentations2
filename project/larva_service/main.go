// larva_service represents the worker nodes. Each worker accepts jobs pushed
// by hatchery_service, "executes" them (simulated), and reports status back
// on its own poll loop (poll_interval_seconds on the job).
package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	pb "orchestrator/proto_gen"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const hatcheryAddr = "http://localhost:8081"

type workerStore struct {
	mu      sync.Mutex
	workers map[string]*pb.Worker
}

var store = &workerStore{workers: make(map[string]*pb.Worker)}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/workers", handleGetOrCreateWorker)
	mux.HandleFunc("/workers/assign", handleAssignJob)

	log.Println("larva_service listening on :8082")
	if err := http.ListenAndServe(":8082", mux); err != nil {
		log.Fatal(err)
	}
}

// handleGetOrCreateWorker: GET returns worker state by id (?id=...),
// POST creates a new worker of the given type and starts its report loop.
func handleGetOrCreateWorker(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		id := r.URL.Query().Get("id")
		store.mu.Lock()
		worker, ok := store.workers[id]
		store.mu.Unlock()
		if !ok {
			http.Error(w, "worker not found", http.StatusNotFound)
			return
		}
		writeProto(w, worker)

	case http.MethodPost:
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
		worker := &pb.Worker{
			Id:            fmt.Sprintf("worker-%d", rand.Intn(1_000_000)),
			Type:          req.Type,
			LastJobUpdate: timestamppb.Now(),
		}
		store.mu.Lock()
		store.workers[worker.Id] = worker
		store.mu.Unlock()
		log.Printf("registered %s (type=%s) for cluster %s", worker.Id, worker.Type, req.ClusterId)
		writeProto(w, worker)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAssignJob: hatchery_service pushes a job onto a worker's queue here.
// The worker then starts a background loop that "runs" the job and reports
// status back to hatchery_service every poll_interval_seconds.
func handleAssignJob(w http.ResponseWriter, r *http.Request) {
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

	store.mu.Lock()
	worker, ok := store.workers[req.WorkerId]
	if ok {
		req.Job.Status = "running"
		worker.Jobs = append(worker.Jobs, req.Job)
		worker.LastJobUpdate = timestamppb.Now()
	}
	store.mu.Unlock()

	if !ok {
		writeProto(w, &pb.AssignJobResponse{Accepted: false})
		return
	}

	go runJob(worker.Id, req.Job)
	writeProto(w, &pb.AssignJobResponse{Accepted: true})
}

// runJob simulates doing the work, then reports completion back up to
// hatchery_service. PollIntervalSeconds controls how long the "work" takes
// before the worker checks in with a result.
func runJob(workerID string, job *pb.WorkerJob) {
	interval := job.PollIntervalSeconds
	if interval <= 0 {
		interval = 1
	}
	time.Sleep(time.Duration(interval * float64(time.Second)))

	report := &pb.ReportJobStatusRequest{
		WorkerId: workerID,
		Task:     job.Task,
		Status:   "completed",
	}
	body, err := proto.Marshal(report)
	if err != nil {
		log.Printf("marshal report: %v", err)
		return
	}
	resp, err := http.Post(hatcheryAddr+"/jobs/report", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		log.Printf("report to hatchery_service failed: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("worker %s reported job %q complete", workerID, job.Task)
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
