package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/cloudx-org/cloudx/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// GeneratedCertKeyPair holds in-memory PEM-encoded certificate and private key.
type GeneratedCertKeyPair struct {
	CertPEM []byte
	KeyPEM  []byte
}

// GenerateSelfSignedCert generates an in-memory X.509 RSA self-signed certificate suitable for TLS/mTLS.
func GenerateSelfSignedCert(hosts ...string) (*GeneratedCertKeyPair, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate rsa private key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(365 * 24 * time.Hour)

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"CloudX Cluster Security"},
			CommonName:   "cloudx-node",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	if len(hosts) == 0 {
		hosts = []string{"127.0.0.1", "localhost"}
	}

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})

	return &GeneratedCertKeyPair{
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
	}, nil
}

// BuildServerTLSConfig parses the config.TLSConfig and creates *tls.Config for gRPC server.
func BuildServerTLSConfig(cfg config.TLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	var cert tls.Certificate
	var err error

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err = tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load server cert/key pair: %w", err)
		}
	} else {
		// Auto-generate self-signed cert if enabled but no files provided
		generated, err := GenerateSelfSignedCert("127.0.0.1", "localhost")
		if err != nil {
			return nil, fmt.Errorf("failed to generate in-memory cert: %w", err)
		}
		cert, err = tls.X509KeyPair(generated.CertPEM, generated.KeyPEM)
		if err != nil {
			return nil, fmt.Errorf("failed to parse generated cert: %w", err)
		}
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// mTLS (Client Authentication)
	if cfg.ClientAuth {
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		if cfg.CAFile != "" {
			caCert, err := os.ReadFile(cfg.CAFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read ca file '%s': %w", cfg.CAFile, err)
			}
			caPool := x509.NewCertPool()
			if !caPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to append ca certs from '%s'", cfg.CAFile)
			}
			tlsConfig.ClientCAs = caPool
		} else {
			// If client auth requested without specific CA, use the server's own cert as accepted CA
			caPool := x509.NewCertPool()
			for _, c := range cert.Certificate {
				if parsed, err := x509.ParseCertificate(c); err == nil {
					caPool.AddCert(parsed)
				}
			}
			tlsConfig.ClientCAs = caPool
		}
	}

	return tlsConfig, nil
}

// BuildClientTLSConfig creates *tls.Config for gRPC clients dialing the control plane.
func BuildClientTLSConfig(cfg config.TLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if cfg.ServerNameOverride != "" {
		tlsConfig.ServerName = cfg.ServerNameOverride
	}

	// Optional client certificate for mTLS
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client cert/key pair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// CA root verification
	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read ca file '%s': %w", cfg.CAFile, err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to append ca certs from '%s'", cfg.CAFile)
		}
		tlsConfig.RootCAs = caPool
	} else if cfg.CertFile == "" {
		// When TLS enabled without custom CA or cert in testing/dev, allow InsecureSkipVerify if local
		tlsConfig.InsecureSkipVerify = true
	}

	return tlsConfig, nil
}

// BuildServerCredentials returns grpc.ServerOption configuring TLS/mTLS.
func BuildServerCredentials(cfg config.TLSConfig) (grpc.ServerOption, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	tlsCfg, err := BuildServerTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	return grpc.Creds(credentials.NewTLS(tlsCfg)), nil
}

// BuildClientCredentials returns grpc.DialOption configuring TLS/mTLS.
func BuildClientCredentials(cfg config.TLSConfig) (grpc.DialOption, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	tlsCfg, err := BuildClientTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)), nil
}

// ExtractClientIdentity extracts the Subject CommonName or SANs from verified TLS peer certificate in context.
func ExtractClientIdentity(peerCert *x509.Certificate) string {
	if peerCert == nil {
		return ""
	}
	if strings.TrimSpace(peerCert.Subject.CommonName) != "" {
		return peerCert.Subject.CommonName
	}
	if len(peerCert.DNSNames) > 0 {
		return peerCert.DNSNames[0]
	}
	return ""
}
