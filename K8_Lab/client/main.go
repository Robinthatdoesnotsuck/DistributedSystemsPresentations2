package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func loadClientTLSCredentials() (credentials.TransportCredentials, error) {
	pemServerCA, err := os.ReadFile("/etc/ssl/certs/grpc/ca.crt")
	if err != nil {
		return nil, err
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(pemServerCA) {
		return nil, fmt.Errorf("failed to add server CA certificate")
	}

	clientCert, err := tls.LoadX509KeyPair("/etc/ssl/certs/grpc/tls.crt", "/etc/ssl/certs/grpc/tls.key")
	if err != nil {
		return nil, err
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      certPool,
		ServerName:   "grpc-server-service", // Matches SAN in Server Cert
		MinVersion:   tls.VersionTLS13,
	}

	return credentials.NewTLS(config), nil
}

func main() {
	creds, err := loadClientTLSCredentials()
	if err != nil {
		log.Fatalf("Failed to load client TLS credentials: %v", err)
	}

	// Connect to Kubernetes internal DNS service address
	target := "grpc-server-service.default.svc.cluster.local:50051"
	
	log.Printf("Connecting to %s with mTLS...", target)
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Keep alive loop for demonstration inside Pod logs
	for {
		log.Println("mTLS Handshake successful! Connected to gRPC Server.")
		time.Sleep(10 * time.Second)
	}
}
