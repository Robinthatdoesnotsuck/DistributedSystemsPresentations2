# Grab the consul-template binary from HashiCorp's official image rather
# than downloading/building it ourselves — it's a statically linked Go
# binary, so copying it into the nginx image below works cleanly.
FROM hashicorp/consul-template:alpine AS consul-template

FROM nginx:alpine
COPY --from=consul-template /bin/consul-template /usr/local/bin/consul-template
COPY templates/nginx.conf.ctmpl /etc/consul-templates/nginx.conf.ctmpl
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh
EXPOSE 8000
ENTRYPOINT ["/docker-entrypoint.sh"]
