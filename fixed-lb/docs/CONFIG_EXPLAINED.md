# Gateway Config, Explained

This walks through `nginx-gateway/nginx.conf` and `haproxy/haproxy.cfg`
section by section — what each block does, and why it's written that way.
The full architecture now looks like this:

```
                                          ┌──> nginx-gateway-1 ─┐
client ──> HAProxy (:8000, :8404 stats) ──┤                     ├──> backend pool (backend-1/2/3)
                                          └──> nginx-gateway-2 ─┘

client ──> go-gateway (:8001, single instance, unchanged) ────────> backend pool
```

---

## `nginx-gateway/nginx.conf`

### `events {}`
Required top-level block for nginx's event-handling model. Left empty here
because the defaults are fine at this scale — you'd tune it (worker
connections, etc.) only once you're pushing real production traffic.

### `upstream backend_pool { ... }`
Defines a named pool of backends that a `location` block can send traffic
to. Think of it as the nginx equivalent of your Go gateway's `RouteGroup`
struct — a list of servers plus a policy for picking one.

```nginx
upstream backend_pool {
    least_conn;
    server backend-1:8080 max_fails=3 fail_timeout=30s;
    server backend-2:8080 max_fails=3 fail_timeout=30s;
    server backend-3:8080 max_fails=3 fail_timeout=30s;
}
```

- **`least_conn;`** — the load-balancing policy. Instead of cycling through
  backends in a fixed order (round-robin, which is what happens if you
  delete this line), nginx tracks how many connections each backend
  currently has open and sends the next request to whichever has the
  fewest. This matters when request durations vary — round-robin can pile
  extra work onto a backend that's already slow just because "it's next in
  line"; `least_conn` won't.
- **`max_fails=3 fail_timeout=30s`** — nginx open-source's only health
  checking mechanism, and it's *passive*: nginx doesn't proactively ping
  anything. It just watches real client requests, and if a backend fails 3
  of them within a 30s window, nginx stops sending it new traffic for the
  next 30s, then gives it another chance. Compare this to the Go gateway,
  which actively polls `/heartbeat` every 5 seconds whether or not any real
  traffic is flowing — a meaningfully different failure-detection story.

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

## The one thing to actually go watch happen

1. `docker-compose up --build`
2. Open `http://localhost:8404/stats` in a browser.
3. In another terminal: `docker stop nginx-gateway-1`.
4. Watch the stats page mark `nginx-gateway-1` down within ~10s (two failed
   5s checks), while `curl localhost:8000/items` keeps working the whole
   time — routed to `nginx-gateway-2` without you doing anything.
5. `docker start nginx-gateway-1` and watch it get marked back up and
   rejoin the rotation.

That's the actual payoff of this step: the gateway layer can now lose an
instance without the system going down, and you can see it happen instead
of taking it on faith.
