mkdir -p certs

# 1. Certificate Authority (CA)
openssl genrsa -out certs/ca.key 4096
openssl req -new -x509 -days 3650 -key certs/ca.key -out certs/ca.crt -subj "/CN=MyK8sCA"

# 2. Server Key & Cert (with K8s Service DNS in SAN)
openssl genrsa -out certs/server.key 2048
openssl req -new -key certs/server.key -out certs/server.csr -subj "/CN=grpc-server-service"
openssl x509 -req -days 365 -in certs/server.csr \
  -CA certs/ca.crt -CAkey certs/ca.key -CAcreateserial -out certs/server.crt \
  -extfile <(printf "subjectAltName=DNS:grpc-server-service,DNS:grpc-server-service.default.svc.cluster.local,IP:127.0.0.1")

# 3. Client Key & Cert
openssl genrsa -out certs/client.key 2048
openssl req -new -key certs/client.key -out certs/client.csr -subj "/CN=grpc-client"
openssl x509 -req -days 365 -in certs/client.csr \
  -CA certs/ca.crt -CAkey certs/ca.key -CAcreateserial -out certs/client.crt

rm certs/*.csr certs/*.srl
