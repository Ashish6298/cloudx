package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/api"
	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/cloudx-org/cloudx/internal/worker"
)

func TestTokenValidator_BasicAndTTL(t *testing.T) {
	clusterID := "test-cluster-alpha"
	tv := auth.NewTokenValidator(clusterID)

	if tv.ClusterID() != clusterID {
		t.Fatalf("expected clusterID %s, got %s", clusterID, tv.ClusterID())
	}

	// 1. Static valid token
	token1 := "clx-btk-static-1234"
	tv.AddToken(token1, 0)

	res1 := tv.Validate(token1, clusterID)
	if !res1.Valid {
		t.Fatalf("expected token1 to be valid, got invalid: %s", res1.Reason)
	}

	// 2. Token with expired TTL
	token2 := "clx-btk-expired-5678"
	tv.AddToken(token2, 50*time.Millisecond)
	time.Sleep(75 * time.Millisecond)

	res2 := tv.Validate(token2, clusterID)
	if res2.Valid {
		t.Fatalf("expected token2 with 50ms TTL to be expired and invalid")
	}

	// 3. Token with valid future TTL
	token3Obj, err := tv.GenerateToken(2 * time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	res3 := tv.Validate(token3Obj.Token, clusterID)
	if !res3.Valid {
		t.Fatalf("expected generated token to be valid: %s", res3.Reason)
	}

	// 4. Unknown cluster ID rejection
	res4 := tv.Validate(token1, "different-unknown-cluster")
	if res4.Valid {
		t.Fatalf("expected rejection when target cluster ID is unknown")
	}

	// 5. Revoked token
	tv.RevokeToken(token1)
	res5 := tv.Validate(token1, clusterID)
	if res5.Valid {
		t.Fatalf("expected revoked token to be invalid")
	}
}

func TestTokenValidator_Signatures(t *testing.T) {
	secretToken := "clx-btk-secret-xyz"
	workerPayload := "wrk-12345:192.168.1.50:7001"

	sig := auth.GenerateSignature(secretToken, workerPayload)
	if sig == "" {
		t.Fatalf("expected non-empty signature")
	}

	if !auth.VerifySignature(secretToken, workerPayload, sig) {
		t.Fatalf("expected signature verification to pass")
	}

	if auth.VerifySignature("wrong-secret", workerPayload, sig) {
		t.Fatalf("expected signature verification to fail with wrong secret")
	}
}

func TestControlPlane_AuthenticationScenarios(t *testing.T) {
	ctx := context.Background()
	clusterID := "cloudx-corp-prod"
	validToken := "clx-btk-corp-valid-key"
	expiredToken := "clx-btk-corp-expired-key"

	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer store.Close()

	tv := auth.NewTokenValidator(clusterID)
	tv.AddToken(validToken, 10*time.Minute)
	tv.AddToken(expiredToken, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond) // expire the token

	srv, err := api.NewServer(api.ServerOptions{
		Address:        "127.0.0.1:0",
		ClusterID:      clusterID,
		Store:          store,
		Logger:         logging.NewDefaultLogger(),
		TokenValidator: tv,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop()

	serverAddr := srv.Address()

	// Scenario 1: Reject Invalid Token
	t.Run("RejectInvalidToken", func(t *testing.T) {
		cfg := config.NewDefaultConfig()
		cfg.Storage.Path = t.TempDir()
		cfg.ControlPlane.Address = serverAddr
		cfg.Worker.BootstrapToken = "completely-wrong-token"
		cfg.Worker.ClusterID = clusterID

		daemon, err := worker.NewDaemon(worker.Options{Config: cfg, Logger: logging.NewDefaultLogger()})
		if err != nil {
			t.Fatalf("failed to create daemon: %v", err)
		}
		joinCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = daemon.Start(joinCtx)
		if err == nil {
			_ = daemon.Stop(context.Background())
			t.Fatalf("expected rejection for invalid token")
		}
		if daemon.Status() != worker.StatusDegraded {
			t.Fatalf("expected DEGRADED status, got %s", daemon.Status())
		}
	})

	// Scenario 2: Reject Expired Token
	t.Run("RejectExpiredToken", func(t *testing.T) {
		cfg := config.NewDefaultConfig()
		cfg.Storage.Path = t.TempDir()
		cfg.ControlPlane.Address = serverAddr
		cfg.Worker.BootstrapToken = expiredToken
		cfg.Worker.ClusterID = clusterID

		daemon, err := worker.NewDaemon(worker.Options{Config: cfg, Logger: logging.NewDefaultLogger()})
		if err != nil {
			t.Fatalf("failed to create daemon: %v", err)
		}
		joinCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = daemon.Start(joinCtx)
		if err == nil {
			_ = daemon.Stop(context.Background())
			t.Fatalf("expected rejection for expired token")
		}
	})

	// Scenario 3: Reject Unknown Cluster
	t.Run("RejectUnknownCluster", func(t *testing.T) {
		cfg := config.NewDefaultConfig()
		cfg.Storage.Path = t.TempDir()
		cfg.ControlPlane.Address = serverAddr
		cfg.Worker.BootstrapToken = validToken
		cfg.Worker.ClusterID = "wrong-cluster-id"

		daemon, err := worker.NewDaemon(worker.Options{Config: cfg, Logger: logging.NewDefaultLogger()})
		if err != nil {
			t.Fatalf("failed to create daemon: %v", err)
		}
		joinCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = daemon.Start(joinCtx)
		if err == nil {
			_ = daemon.Stop(context.Background())
			t.Fatalf("expected rejection for mismatched cluster ID")
		}
	})

	// Scenario 4: Accept Valid Token and Matching Cluster
	t.Run("AcceptValidToken", func(t *testing.T) {
		cfg := config.NewDefaultConfig()
		cfg.Storage.Path = t.TempDir()
		cfg.ControlPlane.Address = serverAddr
		cfg.Worker.Address = "10.0.0.99:7001"
		cfg.Worker.BootstrapToken = validToken
		cfg.Worker.ClusterID = clusterID

		daemon, err := worker.NewDaemon(worker.Options{Config: cfg, Logger: logging.NewDefaultLogger()})
		if err != nil {
			t.Fatalf("failed to create daemon: %v", err)
		}
		joinCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err = daemon.Start(joinCtx)
		if err != nil {
			t.Fatalf("expected valid token join to succeed: %v", err)
		}
		defer daemon.Stop(context.Background())

		if daemon.Status() != worker.StatusReady {
			t.Fatalf("expected READY status, got %s", daemon.Status())
		}
		if daemon.ClusterID() != clusterID {
			t.Fatalf("expected clusterID %s, got %s", clusterID, daemon.ClusterID())
		}
	})

	// Scenario 5: Reject Duplicate Identity from a different address
	t.Run("RejectDuplicateIdentity", func(t *testing.T) {
		// Attempt to use daemon from scenario 4's worker ID from a different address "10.0.0.100:7001"
		workers, _ := store.Workers().List(ctx)
		if len(workers) == 0 {
			t.Fatalf("expected existing worker in store")
		}
		existingWorkerID := workers[0].ID

		cfgDup := config.NewDefaultConfig()
		cfgDupDir := t.TempDir()
		cfgDup.Storage.Path = cfgDupDir
		cfgDup.ControlPlane.Address = serverAddr
		cfgDup.Worker.Address = "10.0.0.100:7001"
		cfgDup.Worker.BootstrapToken = validToken
		cfgDup.Worker.ClusterID = clusterID

		// Force the worker.id file to duplicate existingWorkerID
		idMgr := worker.NewIdentityManager(cfgDupDir)
		_, _ = idMgr.GetOrCreateIdentity(existingWorkerID.String())

		daemonDup, err := worker.NewDaemon(worker.Options{Config: cfgDup, Logger: logging.NewDefaultLogger()})
		if err != nil {
			t.Fatalf("failed to create duplicate daemon: %v", err)
		}
		joinCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = daemonDup.Start(joinCtx)
		if err == nil {
			_ = daemonDup.Stop(context.Background())
			t.Fatalf("expected rejection for duplicate worker identity from different address")
		}
	})
}
