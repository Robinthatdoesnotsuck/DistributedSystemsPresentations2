# Minimal Go Microservices: Two Gateway Stacks + Load-Balanced Backend Pool

```
                                          ┌──> nginx-gateway-1 ─┐
client ──> HAProxy (:8000, :8404 stats) ──┤                     ├──> backend pool (backend-1/2/3)
                                          └──> nginx-gateway-2 ─┘

client ──> go-gateway (:8001, single instance) ────────────────────> backend pool
```

Full config walkthrough (nginx + HAProxy, section by section, in plain
English): **[`docs/CONFIG_EXPLAINED.md`](docs/CONFIG_EXPLAINED.md)**.

## Structure

```
project/
├── docker-compose.yml
├── docs/
│   └── CONFIG_EXPLAINED.md   # nginx.conf + haproxy.cfg, explained section by section
├── gateway/            # hand-rolled Go routing + load-balancing middleware
│   ├── main.go
│   ├── routes.json     # static "service discovery" file, one pool per prefix
│   ├── go.mod
│   └── Dockerfile
├── nginx-gateway/       # reverse proxy + backend LB, done via nginx config (2 replicas)
│   ├── nginx.conf
│   └── Dockerfile
├── haproxy/             # load-balances across the two nginx-gateway replicas
│   ├── haproxy.cfg
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

**nginx gateway (`nginx-gateway/nginx.conf`), two replicas, behind HAProxy on :8000**
- Same idea as the Go gateway, expressed as static nginx config: an
  `upstream backend_pool { ... }` block lists the same three backends.
- Load balancing: `least_conn` — send each request to whichever backend
  currently has the fewest open connections, instead of blind round-robin.
  Round-robin and weighted alternatives are commented in the config to try.
- Health checking: nginx open-source only does **passive** checks against
  `backend_pool` — it doesn't poll `/heartbeat` on its own. `max_fails=3
  fail_timeout=30s` tells it to stop sending traffic to a backend after 3
  real request failures, and retry it after 30s.
- `proxy_pass` + `proxy_set_header` do what `httputil.ReverseProxy` did for
  you automatically in Go, but spelled out explicitly in config.
- **Now runs as two replicas** (`nginx-gateway-1`, `nginx-gateway-2`),
  neither reachable directly from the host — only HAProxy talks to them.

**HAProxy (`haproxy/haproxy.cfg`), the actual front door, on :8000**
- Sits in front of the two nginx-gateway replicas and load-balances across
  *them* (`balance roundrobin`) — a separate concern from the backend-level
  load balancing nginx does. If one nginx-gateway container dies, HAProxy
  routes around it and the system stays up.
- Does **active** health checks (`option httpchk GET /heartbeat`, every 5s)
  — a free HAProxy feature, unlike bare nginx. Watch it live at
  `http://localhost:8404/stats`.
- The Go gateway (`:8001`) is intentionally left as a single instance —
  no HA applied there — so you can see the contrast: one path with gateway
  redundancy, one without.

See `docs/CONFIG_EXPLAINED.md` for the full section-by-section breakdown of
both config files, including exactly what each directive does and why.

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
# Watch requests get distributed across backend-1/2/3, via either path
for i in 1 2 3 4 5 6; do curl -s -i localhost:8000/items -X POST -d '{"name":"item"}' | grep X-Instance-Id; done   # HAProxy -> nginx -> backends
for i in 1 2 3 4 5 6; do curl -s -i localhost:8001/items -X POST -d '{"name":"item"}' | grep X-Instance-Id; done   # Go gateway -> backends

# List / get / update / delete work the same through either path
curl localhost:8000/items
curl localhost:8000/items/1
curl -X PUT localhost:8000/items/1 -d '{"name":"gadget"}'
curl -X DELETE localhost:8000/items/1

# Each layer's own heartbeat
curl localhost:8000/heartbeat   # hits HAProxy -> whichever nginx-gateway replica it picks
curl localhost:8001/heartbeat   # Go gateway

# HAProxy's live dashboard: per-replica request counts, up/down status
open http://localhost:8404/stats

# Only the Go gateway exposes per-backend health as JSON, since it actively polls
curl localhost:8001/status
```

## Things worth trying next (educational extensions)

- `docker stop nginx-gateway-1` while watching `http://localhost:8404/stats`
  — see it get marked down within ~10s, traffic keeps flowing through
  `nginx-gateway-2`, then `docker start nginx-gateway-1` to see it rejoin.
- Stop `backend-2` and hit `/items` through nginx a few times: you should
  see a couple of failed/slow requests before nginx marks it down for
  `fail_timeout`, versus HAProxy or the Go gateway, both of which notice
  within one health-check interval — passive vs. active checking, at two
  different layers now.
- Swap `least_conn` back to round-robin (just delete the line) or try
  `ip_hash` in `nginx.conf`, and compare the distribution pattern via the
  `X-Instance-Id` header.
- Hammer `POST /items` with `hey`/`ab`/`wrk` to see the store's mutex
  serialize writes while `GET /items` reads stay fast.
- Give the backends a shared datastore (Postgres/Redis) so the pool
  actually behaves like one logical service instead of three isolated ones.
- Once nginx's static `upstream` block feels familiar, swap it for
  Traefik: same load-balancing/health-check concepts, but backends are
  discovered automatically from Docker labels instead of hardcoded in
  `nginx.conf`.
