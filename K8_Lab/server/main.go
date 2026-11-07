package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func loadServerTLSCredentials() (credentials.TransportCredentials, error) {
	// 1. Load CA certificate (mounted from Secret)
	pemClientCA, err := os.ReadFile("/etc/ssl/certs/grpc/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemClientCA) {
		return nil, fmt.Errorf("failed to add client CA certificate")
	}

	// 2. Load Server cert & key (mounted from Secret)
	serverCert, err := tls.LoadX509KeyPair("/etc/ssl/certs/grpc/tls.crt", "/etc/ssl/certs/grpc/tls.key")
	if err != nil {
		return nil, fmt.Errorf("failed to load server key pair: %w", err)
	}

	// 3. Configure mTLS
	config := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    certPool,
		MinVersion:   tls.VersionTLS13,
	}

	return credentials.NewTLS(config), nil
}

func main() {
	creds, err := loadServerTLSCredentials()
	if err != nil {
		log.Fatalf("TLS setup failed: %v", err)
	}

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(creds))
	
	log.Println("gRPC Server running on port 50051 with mTLS...")
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
