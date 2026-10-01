#!/bin/sh
set -e

CONSUL_ADDR="${CONSUL_HTTP_ADDR:-consul:8500}"

# Render once before nginx even starts, so nginx never boots pointed at an
# empty or half-written config file.
consul-template \
  -consul-addr "${CONSUL_ADDR}" \
  -template "/etc/consul-templates/nginx.conf.ctmpl:/etc/nginx/nginx.conf" \
  -once

# Start nginx in the background...
nginx -g 'daemon off;' &

# ...then run consul-template as the foreground process. It stays running,
# re-renders /etc/nginx/nginx.conf whenever Consul's view of the "backend"
# service changes, and runs `nginx -s reload` after every re-render — a
# reload swaps config without dropping in-flight connections, unlike a
# restart.
exec consul-template \
  -consul-addr "${CONSUL_ADDR}" \
  -template "/etc/consul-templates/nginx.conf.ctmpl:/etc/nginx/nginx.conf:nginx -s reload"
