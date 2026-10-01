# Minimal Go Microservices: Consul Service Discovery + Two Gateway Stacks

```
                                                          ┌──> nginx-gateway-1 ─┐
client ──> HAProxy (:8000, :8404 stats) ──────────────────┤                     ├──> backend pool (backend-1/2/3)
                                                          └──> nginx-gateway-2 ─┘

client ──> go-gateway (:8001, single instance) ─────────────────────────────────> backend pool

                              ┌─────────────┐
   backend-1/2/3 register ───>│             │<─── go-gateway queries "who's healthy?"
   nginx-gateway-1/2 query ───>│   Consul    │<─── nginx-gateway queries (via consul-template)
                              │  (:8500 UI) │
                              └─────────────┘
```

Full config walkthrough (nginx template + HAProxy, section by section, in
plain English): **[`docs/CONFIG_EXPLAINED.md`](docs/CONFIG_EXPLAINED.md)**.

## Structure

```
project/
├── docker-compose.yml
├── docs/
│   └── CONFIG_EXPLAINED.md   # nginx template + haproxy.cfg, explained section by section
├── gateway/                   # hand-rolled Go routing; discovers backends via Consul
│   ├── main.go
│   ├── routes.json           # now just {prefix -> Consul service name}
│   ├── go.mod
│   └── Dockerfile
├── nginx-gateway/             # reverse proxy + LB, config rendered live from Consul (2 replicas)
│   ├── templates/nginx.conf.ctmpl
│   ├── docker-entrypoint.sh
│   └── Dockerfile
├── haproxy/                   # load-balances across the two nginx-gateway replicas (static, unchanged)
│   ├── haproxy.cfg
│   └── Dockerfile
└── backend/                   # CRUD service; self-registers with Consul (identical image, 3 instances)
    ├── main.go
    ├── go.mod
    └── Dockerfile
```

## How it works

**Consul (`consul` container), on :8500**
- Runs in single-node "dev mode" — in-memory, no ACLs, no persistence.
  It's the new source of truth for "which backend instances exist and are
  currently healthy," replacing both the old static target lists AND the
  hand-written heartbeat-polling code that used to live in every gateway.
- Web UI at `http://localhost:8500/ui` — open it and watch `backend-1/2/3`
  show up as they register, and flip between healthy/unhealthy live.

**Backend (`backend/main.go`)**
- Still the same in-memory CRUD store guarded by `sync.RWMutex`.
- New: on startup, each instance **self-registers** with Consul via
  `PUT /v1/agent/service/register` — "I'm `backend-2`, reach me at
  `backend-2:8080`, and here's my health check: hit `/heartbeat` every 5s."
  Consul takes over the health polling from there; no gateway does its own
  polling of backends anymore.
- On shutdown (`docker stop`, which sends `SIGTERM`), it **deregisters
  itself** from Consul before actually exiting, so it stops receiving
  traffic immediately rather than waiting for Consul's own timeout.
- Still stamps responses with `X-Instance-Id` so you can watch either
  gateway spread requests across instances.

**Go gateway (`gateway/main.go`), on :8001**
- `routes.json` no longer lists IPs or ports — it's just
  `{"prefix": "/items", "service": "backend"}`. The mapping from a path to
  a *logical service name* is the only thing that's still static; the
  actual instances behind that name are fully dynamic now.
- Every 5s (`startDiscoveryLoop`), it asks Consul directly: "give me every
  instance of `backend` that's currently passing its health check"
  (`GET /v1/health/service/backend?passing=true`). Whatever comes back
  becomes the new pool for round-robin selection.
- The gateway's own heartbeat-polling code from earlier versions is gone —
  Consul already did that work, once, for every consumer. `GET /status`
  now just reflects Consul's last answer.
- Still layers a worker-pool semaphore on top of net/http's default
  per-request concurrency, unchanged from before.

**nginx gateway (`nginx-gateway/`), two replicas, behind HAProxy on :8000**
- `nginx.conf` is no longer a static file — it's a *template*
  (`templates/nginx.conf.ctmpl`) rendered by **consul-template**, a small
  daemon bundled into the same container.
- On container start, `docker-entrypoint.sh` renders the template once
  (so nginx never boots with an empty config), starts nginx, then leaves
  consul-template running in the foreground. From then on, any time
  Consul's view of the `backend` service changes, consul-template
  re-renders `nginx.conf` and runs `nginx -s reload` — a graceful reload,
  not a restart, so in-flight connections aren't dropped.
- The `upstream backend_pool` block is now populated by
  `{{ range service "backend" }}` instead of three hardcoded `server`
  lines — Consul (via `passing=true` semantics, the function's default)
  is the one deciding what goes in that list. The `least_conn;`
  load-balancing policy from the previous step is untouched — that's a
  policy decision, independent of how the list gets populated.

**HAProxy (`haproxy/haproxy.cfg`), the actual front door, on :8000**
- Unchanged from before, and deliberately so: it still points at
  `nginx-gateway-1`/`nginx-gateway-2` by their static Docker service names.
  That's a short, rarely-changing list (a deployment decision), unlike the
  backend pool, which is exactly the kind of thing that scales dynamically
  and benefits from Consul. Not every list needs to be dynamic — this is
  a deliberate scope boundary, not an oversight.

See `docs/CONFIG_EXPLAINED.md` for the full section-by-section breakdown of
the nginx template and HAProxy config.

**Caveat, still true:** each backend instance has its own private in-memory
store — no shared database — so an item created on one instance won't show
up when a later request happens to land on another.

## Run it

```bash
docker-compose up --build
```

Consul takes a moment to be ready, and backends take a moment to register
— give it 5-10 seconds after `docker-compose up` before hammering the
gateways.

## Try it

```bash
# Watch instances register/deregister live
open http://localhost:8500/ui

# Watch requests get distributed across backend-1/2/3, via either path
for i in 1 2 3 4 5 6; do curl -s -i localhost:8000/items -X POST -d '{"name":"item"}' | grep X-Instance-Id; done   # HAProxy -> nginx -> Consul-discovered backends
for i in 1 2 3 4 5 6; do curl -s -i localhost:8001/items -X POST -d '{"name":"item"}' | grep X-Instance-Id; done   # Go gateway -> Consul-discovered backends

# List / get / update / delete work the same through either path
curl localhost:8000/items
curl localhost:8000/items/1
curl -X PUT localhost:8000/items/1 -d '{"name":"gadget"}'
curl -X DELETE localhost:8000/items/1

# Each layer's own heartbeat
curl localhost:8000/heartbeat
curl localhost:8001/heartbeat

# HAProxy's live dashboard
open http://localhost:8404/stats

# The Go gateway's current view of Consul's backend list
curl localhost:8001/status

# Query Consul directly, the same way both gateways do
curl "localhost:8500/v1/health/service/backend?passing=true"
```

## Things worth trying next (educational extensions)

- `docker stop backend-2` and watch three things happen roughly together:
  Consul's UI marks it critical, `curl localhost:8001/status` drops it
  from the Go gateway's list within 5s, and `nginx-gateway`'s rendered
  config (`docker exec nginx-gateway-1 cat /etc/nginx/nginx.conf`) drops
  its `server` line too. `docker start backend-2` and watch all three
  pick it back up automatically — no gateway restart, no config edit.
- `docker-compose up --build --scale backend-1=0` isn't meaningful here
  (container names are fixed), but you can literally add a `backend-4`
  block to `docker-compose.yml` with a new `INSTANCE_ID` and watch it join
  the rotation on both gateways without touching `routes.json`,
  `nginx.conf.ctmpl`, or any gateway code — that's the actual payoff of
  this step.
- `docker stop nginx-gateway-1` while watching `http://localhost:8404/stats`
  — traffic keeps flowing through `nginx-gateway-2`.
- Hammer `POST /items` with `hey`/`ab`/`wrk` to see the store's mutex
  serialize writes while `GET /items` reads stay fast.
- Give the backends a shared datastore (Postgres/Redis) so the pool
  actually behaves like one logical service instead of three isolated ones.
- Once this feels familiar, swap Traefik in for the nginx+consul-template
  combo — Traefik has a native Consul Catalog provider, so it does what
  `docker-entrypoint.sh` + `nginx.conf.ctmpl` do here, built in, with no
  template files or reload scripting of your own.
