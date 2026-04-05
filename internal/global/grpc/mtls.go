package hubgrpc

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// MTLSConfig holds paths to the TLS certificates for mutual authentication.
// These are typically provided by cert-manager in a Kubernetes cluster.
type MTLSConfig struct {
	CertFile string // Path to the TLS certificate (PEM).
	KeyFile  string // Path to the TLS private key (PEM).
	CAFile   string // Path to the CA certificate (PEM) used to verify the peer.
}

// ServerCredentials builds gRPC TransportCredentials for the hub server.
// It requires client certificates (mutual TLS).
func ServerCredentials(cfg MTLSConfig) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: load server keypair: %w", err)
	}

	caCert, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("mtls: CA cert is not valid PEM")
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   tls.VersionTLS13,
	}
	return credentials.NewTLS(tlsCfg), nil
}

// ClientCredentials builds gRPC TransportCredentials for a spoke client.
// The spoke presents its own certificate and verifies the hub's certificate against the CA.
func ClientCredentials(cfg MTLSConfig) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: load client keypair: %w", err)
	}

	caCert, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("mtls: CA cert is not valid PEM")
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}
	return credentials.NewTLS(tlsCfg), nil
}
