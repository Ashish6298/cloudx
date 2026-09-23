package api

import (
	"context"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func startTestServer(t *testing.T) (*Server, v1.ControlPlaneServiceClient, func()) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}

	logger := logging.NewDefaultLogger()
	srv, err := NewServer(ServerOptions{
		Address: "127.0.0.1:0", // random available port
		Store:   store,
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Connect gRPC client
	conn, err := grpc.Dial(srv.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		srv.Stop()
		t.Fatalf("failed to dial grpc server: %v", err)
	}

	client := v1.NewControlPlaneServiceClient(conn)

	cleanup := func() {
		_ = conn.Close()
		srv.Stop()
		_ = store.Close()
	}

	return srv, client, cleanup
}

func TestSuccessfulRequests(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// 1. Register Worker
	regResp, err := client.RegisterWorker(ctx, &v1.RegisterWorkerRequest{
		NodeId:   "node-alpha",
		WorkerId: "wrk-alpha-1",
		Address:  "127.0.0.1:7001",
	})
	if err != nil {
		t.Fatalf("RegisterWorker failed: %v", err)
	}
	if !regResp.Accepted {
		t.Errorf("expected worker to be accepted")
	}

	// 2. Heartbeat
	hbResp, err := client.Heartbeat(ctx, &v1.HeartbeatRequest{
		WorkerId:   "wrk-alpha-1",
		Timestamp:  time.Now().UnixNano(),
		CpuUsage:   22.4,
		MemoryUsed: 512 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("Heartbeat failed: %v", err)
	}
	if !hbResp.Acknowledged {
		t.Errorf("expected heartbeat to be acknowledged")
	}

	// 3. Get Worker
	getResp, err := client.GetWorker(ctx, &v1.GetWorkerRequest{
		WorkerId: "wrk-alpha-1",
	})
	if err != nil {
		t.Fatalf("GetWorker failed: %v", err)
	}
	if getResp.Worker.Id != "wrk-alpha-1" || getResp.Worker.Address != "127.0.0.1:7001" {
		t.Errorf("GetWorker mismatch: %+v", getResp.Worker)
	}

	// 4. List Workers
	listResp, err := client.ListWorkers(ctx, &v1.ListWorkersRequest{})
	if err != nil {
		t.Fatalf("ListWorkers failed: %v", err)
	}
	if len(listResp.Workers) != 1 {
		t.Errorf("expected 1 worker, got %d", len(listResp.Workers))
	}

	// 5. Report Task Status
	taskID := id.NewTaskID().String()
	taskResp, err := client.ReportTaskStatus(ctx, &v1.ReportTaskStatusRequest{
		WorkerId: "wrk-alpha-1",
		TaskId:   taskID,
		State:    "STARTING",
		Pid:      1234,
	})
	if err != nil {
		t.Fatalf("ReportTaskStatus failed: %v", err)
	}
	if !taskResp.Acknowledged {
		t.Errorf("expected task status acknowledged")
	}

	// 6. Report Health
	healthResp, err := client.ReportHealth(ctx, &v1.ReportHealthRequest{
		Report: &v1.HealthReport{
			EntityId:  taskID,
			Status:    "HEALTHY",
			Message:   "HTTP /health 200 OK",
			Timestamp: time.Now().Unix(),
		},
	})
	if err != nil {
		t.Fatalf("ReportHealth failed: %v", err)
	}
	if !healthResp.Acknowledged {
		t.Errorf("expected health report acknowledged")
	}
}

func TestInvalidRequests(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// Empty worker_id in registration
	_, err := client.RegisterWorker(ctx, &v1.RegisterWorkerRequest{
		WorkerId: "",
		Address:  "127.0.0.1:7001",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument code, got: %v", err)
	}

	// Empty worker address
	_, err = client.RegisterWorker(ctx, &v1.RegisterWorkerRequest{
		WorkerId: "wrk-1",
		Address:  "",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument code, got: %v", err)
	}
}

func TestUnknownWorker(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// Heartbeat from unregistered worker
	_, err := client.Heartbeat(ctx, &v1.HeartbeatRequest{
		WorkerId: "non-existent-worker",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound code for unknown worker, got: %v", err)
	}

	// Get unknown worker
	_, err = client.GetWorker(ctx, &v1.GetWorkerRequest{
		WorkerId: "non-existent-worker",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound code, got: %v", err)
	}
}

func TestDuplicateWorkerRegistration(t *testing.T) {
	_, client, cleanup := startTestServer(t)
	defer cleanup()
	ctx := context.Background()

	req := &v1.RegisterWorkerRequest{
		WorkerId: "worker-dup-1",
		Address:  "127.0.0.1:7001",
	}

	// First registration succeeds
	_, err := client.RegisterWorker(ctx, req)
	if err != nil {
		t.Fatalf("initial registration failed: %v", err)
	}

	// Second registration with same ID fails with AlreadyExists
	_, err = client.RegisterWorker(ctx, req)
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("expected AlreadyExists code on duplicate registration, got: %v", err)
	}
}

func TestServerGracefulShutdown(t *testing.T) {
	srv, _, cleanup := startTestServer(t)
	defer cleanup()

	srv.Stop()
	// Calling Stop multiple times must not panic
	srv.Stop()
}
