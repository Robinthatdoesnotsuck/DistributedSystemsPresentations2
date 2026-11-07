# Server secret
kubectl create secret generic grpc-server-secret \
  --from-file=tls.crt=certs/server.crt \
  --from-file=tls.key=certs/server.key \
  --from-file=ca.crt=certs/ca.crt

# Client secret
kubectl create secret generic grpc-client-secret \
  --from-file=tls.crt=certs/client.crt \
  --from-file=tls.key=certs/client.key \
  --from-file=ca.crt=certs/ca.crt

# Apply resources

kubectl apply -f k8s/server.yaml
kubectl apply -f k8s/client.yaml

# Verify our mTLS connection

kubectl logs -l app=grpc-client -f
