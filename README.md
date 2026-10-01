# Minimal Go Microservices: Two Gateways + Load-Balanced Backend Pool

Two gateways doing the same job two different ways, in front of the same
backend pool, one Docker network:

```
                          ┌──> backend-1 (:8080)
client → go-gateway (:8001) ──┤
                          ├──> backend-2 (:8080)
client → nginx-gateway (:8000) ──┤
                          └──> backend-3 (:8080)
```

## Structure

```
project/
├── docker-compose.yml
├── gateway/            # hand-rolled Go routing + load-balancing middleware
│   ├── main.go
│   ├── routes.json     # static "service discovery" file, one pool per prefix
│   ├── go.mod
│   └── Dockerfile
├── nginx-gateway/       # same job, done with nginx config instead of code
│   ├── nginx.conf
│   └── Dockerfile
└── backend/             # CRUD service (identical image, 3 running instances)
    ├── main.go
    ├── go.mod
    └── Dockerfile
```

## How it works

**Go gateway (`gateway/main.go`), on :8001**
- Reads `routes.json` at startup — each entry is `{prefix, targets: [...]}`,
  a pool of backend URLs behind one prefix. Flat file = static service
  discovery.
- Round-robin load balancing via an atomic counter (`RouteGroup.next()`),
  skipping any backend whose `healthy` flag is currently false.
- A worker-pool semaphore caps concurrent in-flight proxied requests.
- A background goroutine actively polls every backend's `/heartbeat` every
  5s, in parallel, and flips each backend's health flag. See `GET /status`.
- Exposes its own `GET /heartbeat`.

**nginx gateway (`nginx-gateway/nginx.conf`), on :8000**
- Same idea, expressed as static nginx config instead of Go code:
  an `upstream backend_pool { ... }` block lists the same three backends.
- Load balancing: nginx defaults to round-robin with no directive needed —
  the exact same policy the Go gateway implements by hand. `least_conn` and
  `ip_hash` are commented out in the config as alternatives to try.
- Health checking: nginx open-source only does **passive** checks — it
  doesn't poll `/heartbeat` on its own like the Go gateway does. Instead,
  `max_fails=3 fail_timeout=30s` tells it to stop sending traffic to a
  backend after 3 real request failures, and retry it after 30s. That's a
  meaningful difference from the Go gateway's active polling, worth noticing.
- `proxy_pass` + `proxy_set_header` do what `httputil.ReverseProxy` did for
  you automatically in Go, but spelled out explicitly in config.

Comparing the two side by side is really the point of this step: same
job, same backend pool, very different amount of code — and a couple of
real behavioral differences (active vs. passive health checks) that are
worth noticing before moving on to Traefik, which gets you dynamic service
discovery *and* active health checks without hand-written config or code.

**Backend (`backend/main.go`)**
- In-memory CRUD store guarded by `sync.RWMutex` to prevent data races
  across the goroutines `net/http` spins up per request.
- Each instance stamps responses with `X-Instance-Id` (from an env var) so
  you can watch either gateway spread requests across `backend-1/2/3`.
- Routes: `GET/POST /items`, `GET/PUT/DELETE /items/{id}`, `GET /heartbeat`.

**Caveat, still true:** each backend instance has its own private in-memory
store — no shared database — so an item created on one instance won't show
up when a later request happens to land on another. Same known limitation
as before.

## Run it

```bash
docker-compose up --build
```

## Try it

```bash
# Watch requests round-robin across backend-1/2/3, via either gateway
for i in 1 2 3 4 5 6; do curl -s -i localhost:8000/items -X POST -d '{"name":"item"}' | grep X-Instance-Id; done   # nginx
for i in 1 2 3 4 5 6; do curl -s -i localhost:8001/items -X POST -d '{"name":"item"}' | grep X-Instance-Id; done   # Go

# List / get / update / delete work the same through either gateway
curl localhost:8000/items
curl localhost:8000/items/1
curl -X PUT localhost:8000/items/1 -d '{"name":"gadget"}'
curl -X DELETE localhost:8000/items/1

# Each gateway's own heartbeat
curl localhost:8000/heartbeat   # nginx
curl localhost:8001/heartbeat   # Go

# Only the Go gateway exposes per-backend health, since it actively polls
curl localhost:8001/status
```

## Things worth trying next (educational extensions)

- Stop `backend-2` and hit `/items` through nginx a few times: you should
  see a couple of failed/slow requests before nginx marks it down for
  `fail_timeout`, versus the Go gateway which notices within one 5s
  heartbeat tick — a concrete illustration of passive vs. active health
  checking.
- Swap nginx's implicit round-robin for `least_conn` or `ip_hash` in
  `nginx.conf` and compare the distribution against the Go gateway's
  round-robin via the `X-Instance-Id` header.
- Hammer `POST /items` with `hey`/`ab`/`wrk` to see the store's mutex
  serialize writes while `GET /items` reads stay fast.
- Give the backends a shared datastore (Postgres/Redis) so the pool
  actually behaves like one logical service instead of three isolated ones.
- Once nginx's static `upstream` block feels familiar, swap it for
  Traefik: same round-robin/health-check concepts, but backends are
  discovered automatically from Docker labels instead of hardcoded in
  `nginx.conf`.
