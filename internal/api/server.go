package api

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/transitions"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Server is the gRPC API server for CloudX Control Plane.
type Server struct {
	v1.UnimplementedControlPlaneServiceServer
	mu         sync.RWMutex
	store      state.Store
	logger     logging.Logger
	grpcServer *grpc.Server
	listener   net.Listener
	address    string
}

// ServerOptions configures the gRPC server.
type ServerOptions struct {
	Address string
	Store   state.Store
	Logger  logging.Logger
}

// NewServer creates a new gRPC Server instance.
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("state.Store is required")
	}
	if opts.Logger == nil {
		opts.Logger = logging.NewDefaultLogger()
	}

	s := &Server{
		store:   opts.Store,
		logger:  opts.Logger,
		address: opts.Address,
	}

	// Logging & Request ID Interceptor
	unaryInterceptor := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		reqID := extractOrGenerateRequestID(ctx)
		start := time.Now()
		reqLogger := s.logger.With("request_id", reqID).With("rpc_method", info.FullMethod)

		reqLogger.Debug("Received gRPC request")
		resp, err := handler(ctx, req)
		duration := time.Since(start)

		if err != nil {
			reqLogger.Error("gRPC request failed in %v: %v", duration, err)
		} else {
			reqLogger.Debug("gRPC request completed successfully in %v", duration)
		}
		return resp, err
	}

	s.grpcServer = grpc.NewServer(grpc.UnaryInterceptor(unaryInterceptor))
	v1.RegisterControlPlaneServiceServer(s.grpcServer, s)

	return s, nil
}

func extractOrGenerateRequestID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if ids := md.Get("x-request-id"); len(ids) > 0 && ids[0] != "" {
			return ids[0]
		}
	}
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}

// Start boots the gRPC server on the configured address.
func (s *Server) Start() error {
	lis, err := net.Listen("tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.address, err)
	}

	s.mu.Lock()
	s.listener = lis
	s.address = lis.Addr().String()
	s.mu.Unlock()

	s.logger.Info("Control Plane gRPC Server listening on %s", s.address)
	go func() {
		if err := s.grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			s.logger.Error("gRPC server error: %v", err)
		}
	}()

	return nil
}

// Address returns the actual bound listening address.
func (s *Server) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.address
}

// Stop gracefully terminates the gRPC server.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grpcServer != nil {
		s.logger.Info("Gracefully stopping Control Plane gRPC Server...")
		s.grpcServer.GracefulStop()
	}
}

// --- gRPC Endpoint Implementations ---

func (s *Server) RegisterWorker(ctx context.Context, req *v1.RegisterWorkerRequest) (*v1.RegisterWorkerResponse, error) {
	if strings.TrimSpace(req.WorkerId) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id must not be empty")
	}
	if strings.TrimSpace(req.Address) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker address must not be empty")
	}

	// Validate node exists or create placeholder if node_id provided
	nodeID := id.ID(req.NodeId)
	if nodeID == "" {
		nodeID = id.NewNodeID()
	}

	_, err := s.store.Nodes().Get(ctx, nodeID)
	if err != nil {
		now := time.Now().UTC()
		_ = s.store.Nodes().Create(ctx, &models.Node{
			ID:        nodeID,
			Name:      "node-" + string(nodeID),
			Address:   req.Address,
			Status:    "READY",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	workerID := id.ID(req.WorkerId)
	// Check for duplicate registration
	existing, _ := s.store.Workers().Get(ctx, workerID)
	if existing != nil {
		return nil, status.Errorf(codes.AlreadyExists, "worker %s is already registered", req.WorkerId)
	}

	now := time.Now().UTC()
	worker := &models.Worker{
		ID:        workerID,
		NodeID:    nodeID,
		Address:   req.Address,
		Status:    "READY",
		Heartbeat: now,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.store.Workers().Create(ctx, worker); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to persist worker: %v", err)
	}

	return &v1.RegisterWorkerResponse{
		Accepted:     true,
		Message:      "Worker registered successfully",
		RegisteredAt: now.Unix(),
	}, nil
}

func (s *Server) Heartbeat(ctx context.Context, req *v1.HeartbeatRequest) (*v1.HeartbeatResponse, error) {
	if strings.TrimSpace(req.WorkerId) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id must not be empty")
	}

	workerID := id.ID(req.WorkerId)
	worker, err := s.store.Workers().Get(ctx, workerID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "unknown worker %s", req.WorkerId)
	}

	worker.Heartbeat = time.Now().UTC()
	worker.Status = "READY"
	if err := s.store.Workers().Update(ctx, worker); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update worker heartbeat: %v", err)
	}

	return &v1.HeartbeatResponse{
		Acknowledged:   true,
		NextIntervalMs: 5000,
	}, nil
}

func (s *Server) GetWorker(ctx context.Context, req *v1.GetWorkerRequest) (*v1.GetWorkerResponse, error) {
	if strings.TrimSpace(req.WorkerId) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id must not be empty")
	}

	w, err := s.store.Workers().Get(ctx, id.ID(req.WorkerId))
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "worker %s not found", req.WorkerId)
	}

	return &v1.GetWorkerResponse{
		Worker: &v1.Worker{
			Id:            w.ID.String(),
			NodeId:        w.NodeID.String(),
			Address:       w.Address,
			Status:        w.Status,
			LastHeartbeat: w.Heartbeat.Unix(),
			CreatedAt:     w.CreatedAt.Unix(),
			UpdatedAt:     w.UpdatedAt.Unix(),
		},
	}, nil
}

func (s *Server) ListWorkers(ctx context.Context, req *v1.ListWorkersRequest) (*v1.ListWorkersResponse, error) {
	workers, err := s.store.Workers().List(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list workers: %v", err)
	}

	var protoWorkers []*v1.Worker
	for _, w := range workers {
		protoWorkers = append(protoWorkers, &v1.Worker{
			Id:            w.ID.String(),
			NodeId:        w.NodeID.String(),
			Address:       w.Address,
			Status:        w.Status,
			LastHeartbeat: w.Heartbeat.Unix(),
			CreatedAt:     w.CreatedAt.Unix(),
			UpdatedAt:     w.UpdatedAt.Unix(),
		})
	}

	return &v1.ListWorkersResponse{Workers: protoWorkers}, nil
}

func (s *Server) ReportTaskStatus(ctx context.Context, req *v1.ReportTaskStatusRequest) (*v1.ReportTaskStatusResponse, error) {
	if strings.TrimSpace(req.WorkerId) == "" || strings.TrimSpace(req.TaskId) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id and task_id must not be empty")
	}

	taskID := id.ID(req.TaskId)
	task, err := s.store.Tasks().Get(ctx, taskID)
	if err != nil {
		// Auto-insert if task not found yet
		now := time.Now().UTC()
		task = &models.Task{
			ID:        taskID,
			WorkerID:  id.ID(req.WorkerId),
			State:     req.State,
			PID:       int(req.Pid),
			ExitCode:  int(req.ExitCode),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.store.Tasks().Create(ctx, task); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to record task: %v", err)
		}
		return &v1.ReportTaskStatusResponse{Acknowledged: true}, nil
	}

	// Validate transition
	targetState := models.TaskState(req.State)
	if err := transitions.Validate(models.TaskState(task.State), targetState); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "invalid task state transition: %v", err)
	}

	task.State = string(targetState)
	task.PID = int(req.Pid)
	task.ExitCode = int(req.ExitCode)
	if err := s.store.Tasks().Update(ctx, task); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update task status: %v", err)
	}

	return &v1.ReportTaskStatusResponse{Acknowledged: true}, nil
}

func (s *Server) ReportHealth(ctx context.Context, req *v1.ReportHealthRequest) (*v1.ReportHealthResponse, error) {
	if req.Report == nil || strings.TrimSpace(req.Report.EntityId) == "" {
		return nil, status.Error(codes.InvalidArgument, "health report and entity_id are required")
	}

	// Append health event into Event repository
	now := time.Now().UTC()
	_ = s.store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "HEALTH_REPORT_" + req.Report.Status,
		Source:    "worker",
		EntityID:  id.ID(req.Report.EntityId),
		Payload:   fmt.Sprintf(`{"status":"%s","message":"%s"}`, req.Report.Status, req.Report.Message),
		CreatedAt: now,
	})

	return &v1.ReportHealthResponse{Acknowledged: true}, nil
}
