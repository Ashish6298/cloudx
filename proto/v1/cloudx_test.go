package v1_test

import (
	"testing"
	"time"

	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"google.golang.org/protobuf/proto"
)

func TestProtoSerializationEntities(t *testing.T) {
	now := time.Now().Unix()

	// 1. Node
	node := &v1.Node{
		Id:        "node-001",
		Name:      "master-node",
		Address:   "127.0.0.1:7000",
		Status:    "READY",
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, err := proto.Marshal(node)
	if err != nil {
		t.Fatalf("failed to marshal Node: %v", err)
	}

	var nodeUnmarshaled v1.Node
	if err := proto.Unmarshal(data, &nodeUnmarshaled); err != nil {
		t.Fatalf("failed to unmarshal Node: %v", err)
	}
	if nodeUnmarshaled.Name != "master-node" || nodeUnmarshaled.Id != "node-001" {
		t.Errorf("unmarshaled Node mismatch: %+v", nodeUnmarshaled)
	}

	// 2. Service
	srv := &v1.Service{
		Id:       "srv-001",
		Name:     "payment-service",
		Replicas: 3,
		Runtime:  "native",
		Command:  "./payment",
		Status:   "HEALTHY",
		SpecJson: `{"env":"prod"}`,
	}
	srvData, err := proto.Marshal(srv)
	if err != nil {
		t.Fatalf("failed to marshal Service: %v", err)
	}

	var srvUnmarshaled v1.Service
	if err := proto.Unmarshal(srvData, &srvUnmarshaled); err != nil {
		t.Fatalf("failed to unmarshal Service: %v", err)
	}
	if srvUnmarshaled.Replicas != 3 || srvUnmarshaled.Name != "payment-service" {
		t.Errorf("unmarshaled Service mismatch: %+v", srvUnmarshaled)
	}

	// 3. Task
	task := &v1.Task{
		Id:        "tsk-001",
		ServiceId: "srv-001",
		WorkerId:  "wrk-001",
		State:     "RUNNING",
		Pid:       4501,
		ExitCode:  0,
	}
	taskData, err := proto.Marshal(task)
	if err != nil {
		t.Fatalf("failed to marshal Task: %v", err)
	}

	var taskUnmarshaled v1.Task
	if err := proto.Unmarshal(taskData, &taskUnmarshaled); err != nil {
		t.Fatalf("failed to unmarshal Task: %v", err)
	}
	if taskUnmarshaled.Pid != 4501 || taskUnmarshaled.State != "RUNNING" {
		t.Errorf("unmarshaled Task mismatch: %+v", taskUnmarshaled)
	}
}

func TestProtoRPCCallMessages(t *testing.T) {
	// Worker Registration Request & Response
	regReq := &v1.RegisterWorkerRequest{
		NodeId:   "node-001",
		WorkerId: "wrk-001",
		Address:  "127.0.0.1:7001",
		Metadata: map[string]string{"cpu_arch": "amd64", "os": "windows"},
	}
	data, err := proto.Marshal(regReq)
	if err != nil {
		t.Fatalf("failed to marshal RegisterWorkerRequest: %v", err)
	}

	var unmarshaledReq v1.RegisterWorkerRequest
	if err := proto.Unmarshal(data, &unmarshaledReq); err != nil {
		t.Fatalf("failed to unmarshal RegisterWorkerRequest: %v", err)
	}
	if unmarshaledReq.WorkerId != "wrk-001" || unmarshaledReq.Metadata["os"] != "windows" {
		t.Errorf("unmarshaled RegisterWorkerRequest mismatch: %+v", unmarshaledReq)
	}

	// Heartbeat Request
	hbReq := &v1.HeartbeatRequest{
		WorkerId:   "wrk-001",
		Timestamp:  time.Now().UnixNano(),
		CpuUsage:   15.4,
		MemoryUsed: 256 * 1024 * 1024,
	}
	hbData, err := proto.Marshal(hbReq)
	if err != nil {
		t.Fatalf("failed to marshal HeartbeatRequest: %v", err)
	}

	var unmarshaledHB v1.HeartbeatRequest
	if err := proto.Unmarshal(hbData, &unmarshaledHB); err != nil {
		t.Fatalf("failed to unmarshal HeartbeatRequest: %v", err)
	}
	if unmarshaledHB.CpuUsage != 15.4 {
		t.Errorf("expected CPU 15.4, got %f", unmarshaledHB.CpuUsage)
	}
}
