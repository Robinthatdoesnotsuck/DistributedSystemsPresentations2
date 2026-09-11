# Minimal Go Microservices: Gateway + Backend

Two services, one Docker network:

```
client → gateway (:8000) → backend (:8080)
```

## Structure

```
project/
├── docker-compose.yml
├── gateway/          # routing middleware
│   ├── main.go
│   ├── routes.json   # static "service discovery" file
│   ├── go.mod
│   └── Dockerfile
└── backend/           # CRUD service
    ├── main.go
    ├── go.mod
    └── Dockerfile
```

## How it works

**Gateway (`gateway/main.go`)**
- Reads `routes.json` at startup — a flat list of `{prefix, target}` pairs.
  This is the "static service discovery": no registry, just a file.
- For every incoming request, it acquires a slot from a buffered channel
  (`sem`) before forwarding it via `httputil.ReverseProxy` — a bounded
  worker-pool pattern layered on top of Go's default one-goroutine-per-request
  model, capping concurrent in-flight proxied requests.
- Runs a background goroutine that, every 5s, fans out one goroutine per
  backend to hit its `/heartbeat` endpoint concurrently, joins them with a
  `sync.WaitGroup`, and records results in a map guarded by `sync.RWMutex`.
  See it at `GET /status`.
- Exposes its own `GET /heartbeat`.

**Backend (`backend/main.go`)**
- In-memory CRUD store (`map[int]Item`) guarded by `sync.RWMutex`. Since
  `net/http` handles each request in its own goroutine, concurrent
  reads/writes to that map would race without the lock — `RLock`/`RUnlock`
  for reads, `Lock`/`Unlock` for writes.
- Routes: `GET/POST /items`, `GET/PUT/DELETE /items/{id}`, `GET /heartbeat`.

## Run it

```bash
docker-compose up --build
```

## Try it

```bash
# Create
curl -X POST localhost:8000/items -d '{"name":"widget"}'

# List
curl localhost:8000/items

# Get one
curl localhost:8000/items/1

# Update
curl -X PUT localhost:8000/items/1 -d '{"name":"gadget"}'

# Delete
curl -X DELETE localhost:8000/items/1

# Gateway's own heartbeat
curl localhost:8000/heartbeat

# What the gateway thinks of backend health (populated after ~5s)
curl localhost:8000/status
```

## Things worth trying next (educational extensions)

- Hammer `POST /items` with `hey`/`ab`/`wrk` to see the store's mutex
  serialize writes while `GET /items` reads stay fast.
- Kill the `backend` container and watch `/status` on the gateway flip
  to `false` within one heartbeat interval.
- Add a second backend + a second route prefix in `routes.json` to see
  routing extend without touching gateway code.
- Swap the static `routes.json` for real service discovery (Consul,
  etcd, or Docker DNS) once the static version feels solid.
