package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"time"

	pb "orchestrator/proto_gen"

	"google.golang.org/protobuf/proto"
)

func post(url string, req, resp proto.Message) {
	body, _ := proto.Marshal(req)
	r, err := http.Post(url, "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		fmt.Println("ERROR posting", url, err)
		return
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		fmt.Println("ERROR", url, r.StatusCode, string(b))
		return
	}
	proto.Unmarshal(b, resp)
}

func main() {
	// 1. create an account
	account := &pb.Account{}
	post("http://localhost:8080/accounts", &pb.CreateAccountRequest{UserName: "ada"}, account)
	fmt.Printf("account: %+v\n", account)

	// 2. create a cluster for that account (user_service -> hatchery_service)
	cluster := &pb.Cluster{}
	post("http://localhost:8080/accounts/clusters", &pb.CreateClusterRequest{AccountId: account.Id}, cluster)
	fmt.Printf("cluster: %+v\n", cluster)

	// 3. register a worker on that cluster (hatchery_service -> larva_service)
	worker := &pb.Worker{}
	post("http://localhost:8081/clusters/workers", &pb.RegisterWorkerRequest{
		ClusterId: cluster.Id,
		Type:      pb.WorkerType_TRANSFORM_WORKER,
	}, worker)
	fmt.Printf("worker: %+v\n", worker)

	// 4. dispatch a job to that worker (hatchery_service -> larva_service)
	assignResp := &pb.AssignJobResponse{}
	post("http://localhost:8081/clusters/dispatch", &pb.AssignJobRequest{
		WorkerId: worker.Id,
		Job: &pb.WorkerJob{
			Task:                "transform-batch-42",
			PollIntervalSeconds: 1,
			Status:              "queued",
		},
	}, assignResp)
	fmt.Printf("dispatch accepted: %v\n", assignResp.Accepted)

	// 5. wait for the worker to finish and report back
	time.Sleep(2 * time.Second)
	fmt.Println("done — check hatchery_service log for the job report")
}
