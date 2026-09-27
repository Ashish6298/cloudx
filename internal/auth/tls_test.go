package auth

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"google.golang.org/grpc"
)

// mockCPService implements minimal control plane service for TLS testing
type mockCPService struct {
	v1.UnimplementedControlPlaneServiceServer
}

func (m *mockCPService) RegisterWorker(ctx context.Context, req *v1.RegisterWorkerRequest) (*v1.RegisterWorkerResponse, error) {
	return &v1.RegisterWorkerResponse{
		Accepted: true,
		Message:  "registered securely",
	}, nil
}

func TestGenerateSelfSignedCert(t *testing.T) {
	certPair, err := GenerateSelfSignedCert("127.0.0.1", "localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}
	if len(certPair.CertPEM) == 0 || len(certPair.KeyPEM) == 0 {
		t.Fatalf("Generated certificate or key PEM is empty")
	}
}

func TestTLSHandshake_SelfSigned(t *testing.T) {
	// 1. Generate in-memory self-signed cert pair
	certPair, err := GenerateSelfSignedCert("127.0.0.1", "localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "server.crt")
	keyFile := filepath.Join(tmpDir, "server.key")

	if err := os.WriteFile(certFile, certPair.CertPEM, 0600); err != nil {
		t.Fatalf("failed to write cert file: %v", err)
	}
	if err := os.WriteFile(keyFile, certPair.KeyPEM, 0600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	// 2. Start TLS gRPC Server
	serverTLSCfg := config.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
	}

	serverCredOpt, err := BuildServerCredentials(serverTLSCfg)
	if err != nil {
		t.Fatalf("BuildServerCredentials failed: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	grpcServer := grpc.NewServer(serverCredOpt)
	v1.RegisterControlPlaneServiceServer(grpcServer, &mockCPService{})
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	// 3. Client connection with CA verification
	clientTLSCfg := config.TLSConfig{
		Enabled:            true,
		CAFile:             certFile,
		ServerNameOverride: "localhost",
	}

	clientCredOpt, err := BuildClientCredentials(clientTLSCfg)
	if err != nil {
		t.Fatalf("BuildClientCredentials failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, lis.Addr().String(), clientCredOpt, grpc.WithBlock())
	if err != nil {
		t.Fatalf("failed to connect over TLS: %v", err)
	}
	defer conn.Close()

	client := v1.NewControlPlaneServiceClient(conn)
	resp, err := client.RegisterWorker(ctx, &v1.RegisterWorkerRequest{
		WorkerId: "wrk-secure-1",
		Address:  "127.0.0.1:7001",
	})
	if err != nil {
		t.Fatalf("RegisterWorker over TLS failed: %v", err)
	}
	if !resp.Accepted {
		t.Errorf("expected registration to be accepted, got false")
	}
}

func TestMTLS_MutualAuthentication(t *testing.T) {
	// Generate root CA / server cert
	caPair, err := GenerateSelfSignedCert("127.0.0.1", "localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert failed: %v", err)
	}

	tmpDir := t.TempDir()
	caFile := filepath.Join(tmpDir, "ca.crt")
	serverCertFile := filepath.Join(tmpDir, "server.crt")
	serverKeyFile := filepath.Join(tmpDir, "server.key")
	clientCertFile := filepath.Join(tmpDir, "client.crt")
	clientKeyFile := filepath.Join(tmpDir, "client.key")

	_ = os.WriteFile(caFile, caPair.CertPEM, 0600)
	_ = os.WriteFile(serverCertFile, caPair.CertPEM, 0600)
	_ = os.WriteFile(serverKeyFile, caPair.KeyPEM, 0600)

	// Generate client cert signed or self-signed with common CA
	clientPair, err := GenerateSelfSignedCert("worker-client", "localhost")
	if err != nil {
		t.Fatalf("GenerateSelfSignedCert for client failed: %v", err)
	}
	_ = os.WriteFile(clientCertFile, clientPair.CertPEM, 0600)
	_ = os.WriteFile(clientKeyFile, clientPair.KeyPEM, 0600)

	// Server requires mTLS (client cert signed by CA)
	serverTLSCfg := config.TLSConfig{
		Enabled:    true,
		CertFile:   serverCertFile,
		KeyFile:    serverKeyFile,
		CAFile:     clientCertFile, // accept client cert
		ClientAuth: true,
	}

	serverCredOpt, err := BuildServerCredentials(serverTLSCfg)
	if err != nil {
		t.Fatalf("BuildServerCredentials with client auth failed: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	grpcServer := grpc.NewServer(serverCredOpt)
	v1.RegisterControlPlaneServiceServer(grpcServer, &mockCPService{})
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	// 1. Attempt connection WITHOUT client certificate -> Expect failure or reject
	insecureClientTLSCfg := config.TLSConfig{
		Enabled:            true,
		CAFile:             serverCertFile,
		ServerNameOverride: "localhost",
	}
	insecureCredOpt, err := BuildClientCredentials(insecureClientTLSCfg)
	if err != nil {
		t.Fatalf("BuildClientCredentials failed: %v", err)
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	badConn, err := grpc.DialContext(dialCtx, lis.Addr().String(), insecureCredOpt, grpc.WithBlock())
	if err == nil {
		client := v1.NewControlPlaneServiceClient(badConn)
		_, rpcErr := client.RegisterWorker(dialCtx, &v1.RegisterWorkerRequest{WorkerId: "bad-wrk"})
		if rpcErr == nil {
			t.Errorf("expected mTLS handshake or RPC to fail without client certificate")
		}
		badConn.Close()
	}
	dialCancel()

	// 2. Attempt connection WITH valid client certificate -> Expect success
	validClientTLSCfg := config.TLSConfig{
		Enabled:            true,
		CertFile:           clientCertFile,
		KeyFile:            clientKeyFile,
		CAFile:             serverCertFile,
		ServerNameOverride: "localhost",
	}
	validCredOpt, err := BuildClientCredentials(validClientTLSCfg)
	if err != nil {
		t.Fatalf("BuildClientCredentials with client cert failed: %v", err)
	}

	goodCtx, goodCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer goodCancel()

	goodConn, err := grpc.DialContext(goodCtx, lis.Addr().String(), validCredOpt, grpc.WithBlock())
	if err != nil {
		t.Fatalf("mTLS dial with valid client cert failed: %v", err)
	}
	defer goodConn.Close()

	client := v1.NewControlPlaneServiceClient(goodConn)
	resp, err := client.RegisterWorker(goodCtx, &v1.RegisterWorkerRequest{
		WorkerId: "wrk-mtls-verified",
		Address:  "127.0.0.1:7002",
	})
	if err != nil {
		t.Fatalf("RegisterWorker with mTLS failed: %v", err)
	}
	if !resp.Accepted {
		t.Errorf("expected mTLS registration to succeed")
	}
}

func TestSecretRedactionInLogs(t *testing.T) {
	secretToken := "clx-btk-65d4a1b0-9f8e7d6c5b4a3210fedcba"
	rawMsg := "Worker authenticating with token clx-btk-65d4a1b0-9f8e7d6c5b4a3210fedcba and password: MySuperSecretPassword123"

	redacted := logging.RedactSecrets(rawMsg)
	if redacted == rawMsg {
		t.Errorf("expected secrets to be redacted, got unchanged string: %s", redacted)
	}
	if filepath.Base(redacted) == secretToken {
		t.Errorf("secret token leaked in redacted string")
	}

	// Verify private key redaction
	pemKey := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0r...\n-----END RSA PRIVATE KEY-----"
	redactedKey := logging.RedactSecrets(pemKey)
	if redactedKey != "[REDACTED_PRIVATE_KEY]" {
		t.Errorf("expected PEM private key to be replaced with [REDACTED_PRIVATE_KEY], got: %s", redactedKey)
	}

	// Verify field redaction
	redactedField := logging.RedactField("bootstrap_token", secretToken)
	if redactedField != "[REDACTED]" {
		t.Errorf("expected field to be [REDACTED], got: %v", redactedField)
	}
}
