# Minimal Go Microservices: Gateway + Load-Balanced Backend Pool

One gateway, a pool of interchangeable backend instances, one Docker network:

```
                        ┌──> backend-1 (:8080)
client → gateway (:8000)├──> backend-2 (:8080)
                        └──> backend-3 (:8080)
```

## Structure

```
project/
├── docker-compose.yml
├── gateway/          # routing + load-balancing middleware
│   ├── main.go
│   ├── routes.json   # static "service discovery" file, now with a pool per prefix
│   ├── go.mod
│   └── Dockerfile
└── backend/           # CRUD service (identical image, 3 running instances)
    ├── main.go
    ├── go.mod
    └── Dockerfile
```

## How it works

**Gateway (`gateway/main.go`)**
- Reads `routes.json` at startup — each entry is now `{prefix, targets: [...]}`,
  a *pool* of backend URLs behind one prefix instead of a single target. Still
  a flat file, so still "static service discovery" — just one level richer.
- Load balancing: each `RouteGroup` keeps an atomic counter; `next()` does
  round-robin selection across the pool (`counter % len(backends)`), skipping
  any backend whose `healthy` flag is currently false. If every backend in a
  pool is unhealthy, the gateway returns `503` instead of proxying into a
  dead instance.
- For every incoming request, it still acquires a slot from a buffered
  channel (`sem`) before forwarding it — a bounded worker-pool pattern capping
  concurrent in-flight proxied requests, independent of which backend is picked.
- Runs a background goroutine that, every 5s, fans out one goroutine per
  backend to hit `/heartbeat` concurrently, joins with a `sync.WaitGroup`, and
  flips each backend's `atomic.Bool` health flag. Because health lives on the
  `Backend` struct itself (not a shared map), no mutex is needed there. See
  the full picture at `GET /status`.
- Exposes its own `GET /heartbeat`.

**Backend (`backend/main.go`)**
- Same CRUD service as before: in-memory store guarded by `sync.RWMutex`
  (`RLock`/`RUnlock` for reads, `Lock`/`Unlock` for writes) to prevent data
  races across the goroutines `net/http` spins up per request.
- New: each instance reads `INSTANCE_ID` from its environment and stamps
  every response with an `X-Instance-Id` header, so you can literally watch
  the gateway spread requests across `backend-1`, `backend-2`, `backend-3`.
- Routes: `GET/POST /items`, `GET/PUT/DELETE /items/{id}`, `GET /heartbeat`.

**Important caveat:** each backend instance has its *own* in-memory store.
There's no shared database, so `POST`ing an item to instance A won't show up
when a later `GET` happens to land on instance B. That's expected for this
exercise — it's the natural next problem a real system solves with a shared
datastore (Postgres, Redis, etc.), which is a good "what's still missing"
discussion once this is running.

## Run it

```bash
docker-compose up --build
```

## Try it

```bash
# Watch requests round-robin across backend-1/2/3 via the response header
for i in 1 2 3 4 5 6; do curl -s -i localhost:8000/items -X POST -d '{"name":"item"}' | grep X-Instance-Id; done

# List (whichever instance answers this one has only its own items — see caveat above)
curl localhost:8000/items

# Get one
curl localhost:8000/items/1

# Update
curl -X PUT localhost:8000/items/1 -d '{"name":"gadget"}'

# Delete
curl -X DELETE localhost:8000/items/1

# Gateway's own heartbeat
curl localhost:8000/heartbeat

# Per-backend health, per route prefix (populated after ~5s)
curl localhost:8000/status
```

## Things worth trying next (educational extensions)

- Hammer `POST /items` with `hey`/`ab`/`wrk` to see the store's mutex
  serialize writes while `GET /items` reads stay fast.
- `docker stop backend-2` and watch `/status` flip that instance to
  `false`, then watch the round-robin quietly skip it.
- Replace round-robin in `RouteGroup.next()` with a least-connections or
  weighted strategy — the in-flight semaphore count per backend is a
  natural signal to use.
- Give the backends a shared datastore (Postgres/Redis) so the pool
  actually behaves like one logical service instead of three isolated ones.
- Swap the static `routes.json` for real service discovery (Consul,
  etcd, or Docker DNS) once the static version feels solid.
