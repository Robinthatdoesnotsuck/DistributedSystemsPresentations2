# Gateway Config, Explained

This walks through `nginx-gateway/templates/nginx.conf.ctmpl` (rendered
live by consul-template) and `haproxy/haproxy.cfg` section by section —
what each block does, and why it's written that way. The full architecture
now looks like this:

```
                                                          ┌──> nginx-gateway-1 ─┐
client ──> HAProxy (:8000, :8404 stats) ──────────────────┤                     ├──> backend pool (backend-1/2/3)
                                                          └──> nginx-gateway-2 ─┘

client ──> go-gateway (:8001, single instance, unchanged) ─────────────────────> backend pool

Consul (:8500) — backends register into it; both gateway paths read from it
```

---

## `nginx-gateway/templates/nginx.conf.ctmpl` + `docker-entrypoint.sh`

This nginx config is no longer a plain file nginx reads directly — it's a
**template**, re-rendered into the real `nginx.conf` by a small daemon
called **consul-template**, which is bundled into the same container.

### `docker-entrypoint.sh`
```sh
consul-template -consul-addr "${CONSUL_ADDR}" \
  -template "/etc/consul-templates/nginx.conf.ctmpl:/etc/nginx/nginx.conf" \
  -once

nginx -g 'daemon off;' &

exec consul-template -consul-addr "${CONSUL_ADDR}" \
  -template "/etc/consul-templates/nginx.conf.ctmpl:/etc/nginx/nginx.conf:nginx -s reload"
```
- The first `consul-template ... -once` call renders `nginx.conf` a single
  time and exits, **before** nginx starts — this guarantees nginx never
  boots pointed at an empty or half-written file.
- `nginx -g 'daemon off;' &` starts nginx in the background.
- The final `exec consul-template ...` is the one that actually stays
  running. Its `-template` argument has three parts, separated by `:` —
  `source:destination:command` — meaning: watch this source template,
  write it to this destination path, and run this shell command every
  time the rendered output changes. Here that command is `nginx -s
  reload`, which tells the already-running nginx process to reload its
  config gracefully (finish in-flight connections, then swap config) —
  not a restart, so nothing gets dropped.

### `events {}`
Same as before — required top-level block, defaults are fine at this scale.

### `upstream backend_pool { ... }`
```nginx
upstream backend_pool {
    least_conn;
    {{ range service "backend" }}
    server {{ .Address }}:{{ .Port }} max_fails=3 fail_timeout=30s;
    {{ else }}
    server 127.0.0.1:65535 down;
    {{ end }}
}
```
- **`least_conn;`** — unchanged from the previous step: send each request
  to whichever backend currently has the fewest open connections.
- **`{{ range service "backend" }} ... {{ end }}`** — this is
  consul-template's Go-template syntax, not nginx syntax. `service
  "backend"` asks Consul for every instance of the service named
  `backend` that is currently passing its health check (that's the
  default — no `|passing` filter needed, Consul Template only returns
  healthy instances unless you explicitly ask for `"any"`). The loop then
  emits one `server` line per healthy instance, using `.Address` and
  `.Port` from what Consul returned. Add a fourth backend and register it
  with Consul, and a fourth `server` line appears here automatically —
  nobody edits this file by hand.
- **`{{ else }} server 127.0.0.1:65535 down; {{ end }}`** — a fallback for
  the case where Consul currently reports zero healthy backends. nginx
  requires at least one `server` in an `upstream` block to start at all;
  this dummy, permanently-down entry keeps the config valid without
  pretending anything is actually reachable.
- **`max_fails=3 fail_timeout=30s`** — nginx open-source's own passive
  health checking, exactly as before. It's a second, independent safety
  net underneath Consul's active checks: even if consul-template hasn't
  re-rendered yet for some reason, nginx will still stop hammering a
  backend that's actively failing real requests.

### `server { listen 8000; ... }`
The actual HTTP server block — what nginx does when a request comes in.

```nginx
location /items {
    proxy_pass http://backend_pool;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```

- **`proxy_pass http://backend_pool;`** — for any request path matching
  `/items`, hand it off to the upstream pool defined above. This one line
  is doing what `httputil.NewSingleHostReverseProxy` plus your manual
  round-robin/least-conn logic does in the Go gateway.
- **`proxy_set_header ...`** — by default, a reverse proxy talks to the
  backend on its own behalf, so the backend would otherwise see the
  request as if it came from nginx itself, not the real client. These
  headers forward the original client's IP, host, and protocol along, so
  the backend (or its logs) can still tell who actually made the request.

```nginx
location /heartbeat {
    add_header Content-Type text/plain;
    return 200 "nginx gateway alive";
}
```
A static response — this gateway's own liveness check, same purpose as the
Go gateway's `/heartbeat` handler, just declared instead of coded.

---

## `haproxy/haproxy.cfg`

HAProxy's job here is narrower than nginx's: it doesn't know or care about
`/items` — it just spreads traffic across two identical nginx-gateway
containers so neither is a single point of failure.

### `global` / `defaults`
Boilerplate: send logs to stdout (so `docker logs haproxy` shows them), and
set sane timeouts. Not much to explain beyond "every HAProxy config needs
something like this."

### `frontend http_front`
```haproxy
frontend http_front
    bind *:8000
    default_backend nginx_gateways
```
The entry point clients actually hit. "Bind :8000, and send everything to
the `nginx_gateways` backend pool." This is the piece that used to be a
direct connection to one `nginx-gateway` container — now it's one hop
earlier, in front of two.

### `backend nginx_gateways`
```haproxy
backend nginx_gateways
    balance roundrobin
    option httpchk GET /heartbeat
    http-check expect status 200
    server nginx-gateway-1 nginx-gateway-1:8000 check inter 5s fall 2 rise 2
    server nginx-gateway-2 nginx-gateway-2:8000 check inter 5s fall 2 rise 2
```
- **`balance roundrobin`** — at this layer, plain round-robin is fine: both
  nginx-gateway replicas are identical and cheap to route through, so
  there's no `least_conn`-style asymmetry to correct for like there was at
  the backend layer.
- **`option httpchk GET /heartbeat` + `http-check expect status 200`** —
  this is HAProxy *actively* polling each gateway's `/heartbeat`, on its
  own, independent of real traffic. Unlike nginx open-source, active
  health checking is a free, built-in HAProxy feature — worth noticing
  since it's the same capability your Go gateway had to hand-write.
- **`check inter 5s fall 2 rise 2`** on each `server` line — checks every
  5 seconds (matching the Go gateway's interval, for easy comparison),
  requires 2 consecutive failures before marking a replica down (`fall 2`),
  and 2 consecutive successes before trusting it again (`rise 2`). That
  "2 in a row" buffer avoids flapping a replica in and out over one blip.

### `listen stats`
```haproxy
listen stats
    bind *:8404
    stats enable
    stats uri /stats
    stats refresh 5s
```
A built-in dashboard, not required for load balancing to work — it's here
so you can *watch* this happen instead of just reading logs. Open
`http://localhost:8404/stats` while sending traffic and you'll see per-server
request counts, current status (up/down), and check history update live.

---

## Backend registration (`backend/main.go`) + Go gateway discovery (`gateway/main.go`)

These aren't config files, but they're the other half of "how Consul gets
used" here, so worth a quick walkthrough too.

**Registration**, on every backend's startup:
```go
payload := map[string]any{
    "ID":      instanceID,
    "Name":    "backend",
    "Address": instanceID,
    "Port":    8080,
    "Check": map[string]any{
        "HTTP":     fmt.Sprintf("http://%s:8080/heartbeat", instanceID),
        "Interval": "5s",
        "Timeout":  "2s",
        "DeregisterCriticalServiceAfter": "1m",
    },
}
```
sent as `PUT /v1/agent/service/register`. Two things worth noticing:
`ID` is unique per instance (`backend-1`, `backend-2`, ...) while `Name` is
shared (`backend`) — Consul groups everything under `Name` when you query
"give me all instances of `backend`". And the `Check` block is what makes
Consul active: it will hit `http://backend-1:8080/heartbeat` itself, every
5 seconds, without any client ever making a real request.

**Deregistration**, on `SIGTERM` (what `docker stop` sends):
```go
http.NewRequest(http.MethodPut, consulAddr+"/v1/agent/service/deregister/"+instanceID, nil)
```
This is why a stopped backend disappears from Consul (and both gateways)
almost immediately, instead of waiting out `DeregisterCriticalServiceAfter`.

**Discovery**, in the Go gateway, every 5 seconds:
```go
reqURL := fmt.Sprintf("%s/v1/health/service/%s?passing=true", consulAddr, service)
```
`?passing=true` is the Go gateway's equivalent of consul-template's default
`service "backend"` behavior — only currently-healthy instances come back.
Whatever the response contains entirely replaces the route's backend list
for the next round of round-robin picks.

---

## The one thing to actually go watch happen

1. `docker-compose up --build`, then wait ~10s for backends to register.
2. Open `http://localhost:8500/ui` (Consul) and `http://localhost:8404/stats`
   (HAProxy) side by side.
3. `docker stop backend-2`.
4. Watch Consul's UI mark `backend-2` critical within ~5-10s. Then:
   - `curl localhost:8001/status` (Go gateway) should drop it from the list.
   - `docker exec nginx-gateway-1 cat /etc/nginx/nginx.conf` should show its
     `server` line gone, and `docker logs nginx-gateway-1` should show a
     reload happening around the same time.
5. `docker start backend-2` and watch all of it reverse — no gateway
   restart, no config file edited by hand, no code changed.

That's the actual payoff of this step: the set of backend instances is now
a live fact the whole system agrees on, not something copy-pasted into
three different config files that can quietly drift out of sync.
