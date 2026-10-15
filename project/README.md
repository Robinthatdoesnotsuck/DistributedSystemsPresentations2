# orchestrator — a 3-service job-dispatch system

This is a working rebuild of the structure you described (`api/`, `proto_gen/`,
three service directories), implemented as a generic **distributed job
orchestrator** rather than a C2 topology. The shapes map over directly:

| Original (C2-style)                          | Here (generic orchestrator)              |
|-----------------------------------------------|-------------------------------------------|
| `Overmind` (operator + implants)              | `Account` (owns Clusters)                  |
| `Hatchery` (relay / group of implants)        | `Cluster` (group of Workers)               |
| `Larva` (implant / agent)                     | `Worker` (executes jobs)                   |
| `HatcheryInstruction`                         | `ClusterJob`                               |
| `LarvaInstruction` + `sleep_time` (jitter)     | `WorkerJob` + `poll_interval_seconds`      |
| `LarvaType` enum                              | `WorkerType` enum                          |

The `sleep_time`/jitter field specifically is what made the original schema
functionally a beacon-based C2 implant design, so it's reframed here as a
plain **poll interval** — a normal "how often does this worker check in"
knob, with no evasion semantics attached. If you still want a C2-look
*frontend*, point its UI labels at this real, neutral backend (rename
`Worker` → "bot" etc. only in your display layer) rather than building the
jitter/beacon logic for real.

## Layout

```
api/jobs.proto          — protobuf schema (Account, Cluster, Worker, jobs)
proto_gen/jobs.pb.go     — generated Go message types
user_service/            — owns Accounts, talks to hatchery_service   (:8080)
hatchery_service/         — owns Clusters/Workers, talks to larva_service (:8081)
larva_service/            — simulates Worker nodes, reports back to hatchery_service (:8082)
demo/testclient/          — drives the three services end-to-end, for a smoke test
third_party/protobuf-go/  — vendored google.golang.org/protobuf (see note below)
```

## How the services actually message each other

Real gRPC (the way the proto `service` definitions in `api/jobs.proto` are
written) needs the full `google.golang.org/grpc` runtime, which pulls in
`golang.org/x/net`, `x/sys`, `x/text`, etc. Those weren't fetchable from this
sandbox's network allowlist. So instead, each service runs a plain
`net/http` server and the *generated protobuf message types* are
marshaled/unmarshaled directly over HTTP (`application/x-protobuf` bodies) —
same schema, same wire format for the messages, simpler transport. The
`service` blocks in the `.proto` file describe the intended gRPC methods if
you want to regenerate real gRPC stubs later (e.g. on a machine with normal
internet access, just run `protoc --go_out=. --go-grpc_out=. api/jobs.proto`
after installing `protoc-gen-go` / `protoc-gen-go-grpc` normally).

Call flow:
1. `user_service POST /accounts` → creates an `Account`.
2. `user_service POST /accounts/clusters` → forwards to `hatchery_service POST /clusters`, stores the resulting `Cluster` on the account.
3. `hatchery_service POST /clusters/workers` → forwards to `larva_service POST /workers`, which registers a new `Worker` and starts tracking it.
4. `hatchery_service POST /clusters/dispatch` → forwards to `larva_service POST /workers/assign`, which queues a `WorkerJob` on that worker and simulates running it for `poll_interval_seconds`.
5. When the simulated job finishes, `larva_service` calls back to `hatchery_service POST /jobs/report` with the result.

## Running it

```bash
go build -o bin/user_service ./user_service
go build -o bin/hatchery_service ./hatchery_service
go build -o bin/larva_service ./larva_service

./bin/larva_service &
./bin/hatchery_service &
./bin/user_service &

go run ./demo/testclient   # drives the full flow and prints each step
```

You should see log lines in each service's output showing the account →
cluster → worker → job → report chain completing.

## Note on `third_party/protobuf-go`

This sandbox's network egress is locked to a small domain allowlist that
doesn't include the Go module proxy, so `google.golang.org/protobuf` is
vendored locally via a `replace` directive in `go.mod` instead of being
fetched normally. On your own machine with normal internet access you can
drop `third_party/` entirely and just run:

```bash
go mod init orchestrator   # if starting fresh
go get google.golang.org/protobuf@latest
go get google.golang.org/grpc@latest   # if you regenerate real gRPC stubs
```
