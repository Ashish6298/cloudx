package api

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
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
	mu             sync.RWMutex
	store          state.Store
	logger         logging.Logger
	grpcServer     *grpc.Server
	listener       net.Listener
	address        string
	clusterID      string
	tokenValidator *auth.TokenValidator
	tlsConfig         config.TLSConfig
	heartbeatInterval time.Duration
}

// ServerOptions configures the gRPC server.
type ServerOptions struct {
	Address           string
	ClusterID         string
	Store             state.Store
	Logger            logging.Logger
	BootstrapToken    string
	TokenValidator    *auth.TokenValidator
	TLS               config.TLSConfig
	HeartbeatInterval time.Duration
}

// NewServer creates a new gRPC Server instance.
func NewServer(opts ServerOptions) (*Server, error) {
	if opts.Store == nil {
		return nil, fmt.Errorf("state.Store is required")
	}
	if opts.Logger == nil {
		opts.Logger = logging.NewDefaultLogger()
	}

	clusterID := opts.ClusterID
	if clusterID == "" {
		clusterID = "cloudx-cluster-main"
	}

	tv := opts.TokenValidator
	if tv == nil {
		tv = auth.NewTokenValidator(clusterID)
		if opts.BootstrapToken != "" {
			tv.AddToken(opts.BootstrapToken, 0)
		}
	}

	hbInterval := opts.HeartbeatInterval
	if hbInterval <= 0 {
		hbInterval = 5 * time.Second
	}

	s := &Server{
		store:             opts.Store,
		logger:            opts.Logger,
		address:           opts.Address,
		clusterID:         clusterID,
		tokenValidator:    tv,
		tlsConfig:         opts.TLS,
		heartbeatInterval: hbInterval,
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

	serverOpts := []grpc.ServerOption{grpc.UnaryInterceptor(unaryInterceptor)}
	if opts.TLS.Enabled {
		tlsCredOpt, err := auth.BuildServerCredentials(opts.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to configure TLS credentials for gRPC server: %w", err)
		}
		serverOpts = append(serverOpts, tlsCredOpt)
		s.logger.Info("gRPC TLS enabled (clientAuth: %v)", opts.TLS.ClientAuth)
	}

	s.grpcServer = grpc.NewServer(serverOpts...)
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
	if err := auth.ValidateResourceID(req.WorkerId, id.EntityWorker); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid worker_id: %v", err)
	}

	if strings.TrimSpace(req.Address) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker address must not be empty")
	}

	if req.NodeId != "" {
		if err := auth.ValidateResourceID(req.NodeId, id.EntityNode); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid node_id: %v", err)
		}
	}

	// 1. Cluster Bootstrap Token and Target Cluster Authentication
	if s.tokenValidator != nil && s.tokenValidator.HasTokens() {
		reqToken := ""
		if req.Metadata != nil {
			reqToken = req.Metadata["bootstrap_token"]
		}
		targetCluster := ""
		if req.Metadata != nil {
			targetCluster = req.Metadata["cluster_id"]
		}

		res := s.tokenValidator.Validate(reqToken, targetCluster)
		if !res.Valid {
			s.logger.Warn("Worker registration rejected from %s (worker: %s): %s", req.Address, req.WorkerId, res.Reason)
			return &v1.RegisterWorkerResponse{
				Accepted: false,
				Message:  fmt.Sprintf("unauthorized: %s", res.Reason),
			}, nil
		}
	}

	// Validate node exists or create placeholder if node_id provided
	nodeID := id.ID(req.NodeId)
	if nodeID == "" {
		nodeID = id.NewNodeID()
	}

	nodeName := req.Hostname
	if nodeName == "" {
		nodeName = "node-" + string(nodeID)
	}

	_, err := s.store.Nodes().Get(ctx, nodeID)
	if err != nil {
		now := time.Now().UTC()
		_ = s.store.Nodes().Create(ctx, &models.Node{
			ID:        nodeID,
			Name:      nodeName,
			Address:   req.Address,
			Status:    "READY",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	workerID := id.ID(req.WorkerId)
	now := time.Now().UTC()

	// 2. Reject Duplicate Identity across different addresses / different node IDs
	existing, _ := s.store.Workers().Get(ctx, workerID)
	if existing != nil {
		if existing.Address == req.Address {
			// Re-registration / worker restart with same ID and address
			existing.Heartbeat = now
			existing.Status = "READY"
			existing.UpdatedAt = now
			_ = s.store.Workers().Update(ctx, existing)

			return &v1.RegisterWorkerResponse{
				Accepted:            true,
				Message:             "Worker re-registered successfully",
				ClusterId:           s.clusterID,
				RegisteredAt:        now.Unix(),
				HeartbeatIntervalMs: s.heartbeatInterval.Milliseconds(),
				WorkerConfig: map[string]string{
					"cluster_domain": "cloudx.local",
					"log_level":      "info",
				},
			}, nil
		}
		// Duplicate identity detected on another address
		s.logger.Warn("Worker registration rejected: duplicate identity %s from address %s (already registered at %s)", req.WorkerId, req.Address, existing.Address)
		return nil, status.Errorf(codes.AlreadyExists, "duplicate identity: worker %s is already registered with a different address (%s)", req.WorkerId, existing.Address)
	}

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

	// Append WORKER_REGISTERED audit event
	_ = s.store.Events().Append(ctx, &models.Event{
		ID:        id.NewEventID(),
		Type:      "WORKER_REGISTERED",
		Source:    "controlplane_api",
		EntityID:  workerID,
		Payload:   fmt.Sprintf(`{"node_id":"%s","address":"%s","status":"READY"}`, nodeID, req.Address),
		CreatedAt: now,
	})

	return &v1.RegisterWorkerResponse{
		Accepted:            true,
		Message:             "Worker registered successfully",
		ClusterId:           s.clusterID,
		RegisteredAt:        now.Unix(),
		HeartbeatIntervalMs: s.heartbeatInterval.Milliseconds(),
		WorkerConfig: map[string]string{
			"cluster_domain": "cloudx.local",
			"log_level":      "info",
		},
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
	if worker.Status != "DRAINING" && worker.Status != "EMPTY" {
		worker.Status = "READY"
	}
	if err := s.store.Workers().Update(ctx, worker); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update worker heartbeat: %v", err)
	}

	return &v1.HeartbeatResponse{
		Acknowledged:   true,
		NextIntervalMs: s.heartbeatInterval.Milliseconds(),
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

func (s *Server) AssignTask(ctx context.Context, req *v1.TaskAssignmentRequest) (*v1.TaskAssignmentResponse, error) {
	if strings.TrimSpace(req.WorkerId) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if err := auth.ValidateResourceID(req.WorkerId, id.EntityWorker); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid worker_id: %v", err)
	}

	if req.Task == nil || strings.TrimSpace(req.Task.Id) == "" {
		return nil, status.Error(codes.InvalidArgument, "task and task.id are required")
	}
	if err := auth.ValidateResourceID(req.Task.Id, id.EntityTask); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid task.id: %v", err)
	}

	if strings.TrimSpace(req.Command) == "" {
		return nil, status.Error(codes.InvalidArgument, "command is required")
	}

	workerID := id.ID(req.WorkerId)
	worker, err := s.store.Workers().Get(ctx, workerID)
	if err != nil {
		return &v1.TaskAssignmentResponse{
			TaskId:   req.Task.Id,
			Accepted: false,
			Message:  fmt.Sprintf("worker %s not found: %v", req.WorkerId, err),
		}, nil
	}

	if worker.Status != "READY" {
		return &v1.TaskAssignmentResponse{
			TaskId:   req.Task.Id,
			Accepted: false,
			Message:  fmt.Sprintf("worker %s is not READY (status: %s)", req.WorkerId, worker.Status),
		}, nil
	}

	taskID := id.ID(req.Task.Id)
	now := time.Now().UTC()

	// Check if task already exists
	existingTask, _ := s.store.Tasks().Get(ctx, taskID)
	if existingTask != nil {
		if existingTask.WorkerID == workerID && existingTask.State == string(models.TaskStateAssigned) {
			// Idempotent retry
			return &v1.TaskAssignmentResponse{
				TaskId:   req.Task.Id,
				Accepted: true,
				Message:  "Task assignment already recorded",
			}, nil
		}
		if existingTask.State != string(models.TaskStatePending) && existingTask.State != string(models.TaskStateAssigned) {
			return &v1.TaskAssignmentResponse{
				TaskId:   req.Task.Id,
				Accepted: false,
				Message:  fmt.Sprintf("task %s already in state %s", taskID, existingTask.State),
			}, nil
		}
		// Update assignment
		existingTask.WorkerID = workerID
		existingTask.State = string(models.TaskStateAssigned)
		existingTask.UpdatedAt = now
		if err := s.store.Tasks().Update(ctx, existingTask); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to update task assignment: %v", err)
		}
	} else {
		// Persist task in ASSIGNED state before transmission
		task := &models.Task{
			ID:           taskID,
			ServiceID:    id.ID(req.Task.ServiceId),
			JobID:        id.ID(req.Task.JobId),
			DeploymentID: id.ID(req.Task.DeploymentId),
			WorkerID:     workerID,
			State:        string(models.TaskStateAssigned),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.store.Tasks().Create(ctx, task); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to persist task assignment: %v", err)
		}
	}

	return &v1.TaskAssignmentResponse{
		TaskId:   req.Task.Id,
		Accepted: true,
		Message:  "Task assignment successfully persisted and assigned",
	}, nil
}

func (s *Server) ReportTaskStatus(ctx context.Context, req *v1.ReportTaskStatusRequest) (*v1.ReportTaskStatusResponse, error) {
	if strings.TrimSpace(req.WorkerId) == "" || strings.TrimSpace(req.TaskId) == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_id and task_id must not be empty")
	}
	if err := auth.ValidateResourceID(req.WorkerId, id.EntityWorker); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid worker_id: %v", err)
	}
	if err := auth.ValidateResourceID(req.TaskId, id.EntityTask); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid task_id: %v", err)
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

	// If task is associated with a Job, sync Job state
	if task.JobID != "" {
		if job, err := s.store.Jobs().Get(ctx, task.JobID); err == nil && job != nil {
			jobRec, _ := models.JobFromModel(job)
			if jobRec != nil {
				jobRec.ExitCode = task.ExitCode
				switch targetState {
				case models.TaskStateRunning:
					_ = jobRec.Transition(models.JobStateRunning)
				case models.TaskStateStopped:
					if task.ExitCode == 0 {
						_ = jobRec.Transition(models.JobStateSucceeded)
					} else {
						_ = jobRec.Transition(models.JobStateFailed)
					}
				case models.TaskStateFailed, models.TaskStateCrashLoop:
					_ = jobRec.Transition(models.JobStateFailed)
				}
				job.Status = string(jobRec.State)
				job.UpdatedAt = time.Now().UTC()
				if specJSON, err := jobRec.ToSpecJSON(); err == nil {
					job.SpecJSON = specJSON
				}
				_ = s.store.Jobs().Update(ctx, job)
			}
		}
	}

	// Append process/task lifecycle event
	now := time.Now().UTC()
	var eventType string
	switch targetState {
	case models.TaskStateRunning:
		eventType = "PROCESS_STARTED"
	case models.TaskStateStopped:
		eventType = "PROCESS_STOPPED"
	case models.TaskStateFailed:
		eventType = "PROCESS_CRASHED"
	case models.TaskStateCrashLoop:
		eventType = "TASK_CRASH_LOOP"
	case models.TaskStateHealthy:
		eventType = "HEALTH_CHECK_HEALTHY"
	case models.TaskStateUnhealthy:
		eventType = "HEALTH_CHECK_FAILED"
	}

	if eventType != "" {
		_ = s.store.Events().Append(ctx, &models.Event{
			ID:        id.NewEventID(),
			Type:      eventType,
			Source:    "worker_task_manager",
			EntityID:  taskID,
			Payload:   fmt.Sprintf(`{"worker_id":"%s","state":"%s","pid":%d,"exit_code":%d}`, req.WorkerId, req.State, req.Pid, req.ExitCode),
			CreatedAt: now,
		})
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
