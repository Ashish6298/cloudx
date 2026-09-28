package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// TestDatabaseHardening_TransactionsAndRollback verifies ACID rollback guarantees on error.
func TestDatabaseHardening_TransactionsAndRollback(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	nodeID := id.NewNodeID()
	serviceID := id.NewServiceID()

	// Base state
	if err := store.Nodes().Create(ctx, &models.Node{
		ID: nodeID, Name: "base-node", Address: "127.0.0.1:7001", Status: "READY", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("failed to create node: %v", err)
	}

	// Execute failing multi-statement transaction
	txErr := store.Transaction(ctx, func(tx state.Store) error {
		if err := tx.Services().Create(ctx, &models.Service{
			ID: serviceID, Name: "rollback-service", Replicas: 3, Runtime: "native", Command: "app", Status: "PENDING", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}

		// Simulate mid-transaction business error
		return fmt.Errorf("injected failure causing rollback")
	})

	if txErr == nil {
		t.Fatalf("expected transaction to return error")
	}

	// Verify service was NOT created
	_, err = store.Services().Get(ctx, serviceID)
	if err == nil {
		t.Errorf("expected service to be rolled back, but it exists in database")
	}
}

// TestDatabaseHardening_ConcurrentReadWriteIsolation verifies that high-concurrency readers and writers
// operate cleanly without deadlocks, database locks, or torn reads.
func TestDatabaseHardening_ConcurrentReadWriteIsolation(t *testing.T) {
	ctx := context.Background()
	tmpDir, err := os.MkdirTemp("", "cloudx-db-hardening-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "hardened.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open disk store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	nodeID := id.NewNodeID()
	_ = store.Nodes().Create(ctx, &models.Node{
		ID: nodeID, Name: "node-1", Address: "127.0.0.1:7001", Status: "READY", CreatedAt: now, UpdatedAt: now,
	})

	const numWriters = 15
	const numReaders = 25
	const opsPerWorker = 20

	var wg sync.WaitGroup
	errCh := make(chan error, (numWriters+numReaders)*opsPerWorker)

	// Launch concurrent writers
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				svcID := id.NewServiceID()
				err := store.Services().Create(ctx, &models.Service{
					ID:        svcID,
					Name:      fmt.Sprintf("svc-%d-%d", workerIdx, i),
					Replicas:  i + 1,
					Runtime:   "native",
					Command:   "app",
					Status:    "ACTIVE",
					CreatedAt: time.Now().UTC(),
					UpdatedAt: time.Now().UTC(),
				})
				if err != nil {
					errCh <- fmt.Errorf("writer error: %w", err)
					return
				}
			}
		}(w)
	}

	// Launch concurrent readers
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				_, err := store.Services().List(ctx)
				if err != nil {
					errCh <- fmt.Errorf("reader error: %w", err)
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent db error: %v", err)
	}

	// Verify total count
	services, err := store.Services().List(ctx)
	if err != nil {
		t.Fatalf("failed to list services: %v", err)
	}
	if len(services) != numWriters*opsPerWorker {
		t.Errorf("expected %d services, got %d", numWriters*opsPerWorker, len(services))
	}
}

// TestDatabaseHardening_CorruptPartialStateRecovery verifies that invalid schemas or corrupted PRAGMA checks
// are cleanly reported and recoverable.
func TestDatabaseHardening_CorruptPartialStateRecovery(t *testing.T) {
	ctx := context.Background()
	tmpDir, err := os.MkdirTemp("", "cloudx-db-corrupt-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "corrupt_check.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}

	now := time.Now().UTC()
	svcID := id.NewServiceID()
	_ = store.Services().Create(ctx, &models.Service{
		ID: svcID, Name: "payment", Replicas: 2, Runtime: "native", Command: "app", Status: "ACTIVE", CreatedAt: now, UpdatedAt: now,
	})
	store.Close()

	// 1. Re-open and verify state is completely intact
	reopened, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("failed to re-open store: %v", err)
	}
	svc, err := reopened.Services().Get(ctx, svcID)
	if err != nil || svc == nil {
		t.Fatalf("failed to recover service after clean restart: %v", err)
	}
	reopened.Close()

	// 2. Simulate garbage / corrupt header injection
	corruptPath := filepath.Join(tmpDir, "trashed.db")
	_ = os.WriteFile(corruptPath, []byte("GARBAGE_NON_SQLITE_DATA_PADDING_BYTES_1234567890"), 0644)

	_, corruptErr := Open(ctx, corruptPath)
	if corruptErr == nil {
		t.Errorf("expected Open on corrupt database file to fail, but succeeded")
	}
}

// TestDatabaseHardening_MigrationIdempotency verifies that running migrations multiple times is completely safe.
func TestDatabaseHardening_MigrationIdempotency(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	// Run migration a second and third time explicitly
	if err := Migrate(ctx, store.db); err != nil {
		t.Errorf("second migration failed: %v", err)
	}
	if err := Migrate(ctx, store.db); err != nil {
		t.Errorf("third migration failed: %v", err)
	}

	// Verify PRAGMA integrity_check passes cleanly
	var integrityResult string
	row := store.db.QueryRowContext(ctx, "PRAGMA integrity_check")
	if err := row.Scan(&integrityResult); err != nil || integrityResult != "ok" {
		t.Errorf("expected integrity_check = 'ok', got %s (err: %v)", integrityResult, err)
	}
}

// BenchmarkDatabase_TransactionalWrites measures throughput and latency for multi-statement atomic transactions.
func BenchmarkDatabase_TransactionalWrites(b *testing.B) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		b.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		sID := id.NewServiceID()
		tID := id.NewTaskID()
		wID := id.NewWorkerID()

		err := store.Transaction(ctx, func(tx state.Store) error {
			if err := tx.Services().Create(ctx, &models.Service{
				ID: sID, Name: fmt.Sprintf("bench-svc-%d", i), Replicas: 1, Runtime: "native", Command: "run", Status: "ACTIVE", CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
			return tx.Tasks().Create(ctx, &models.Task{
				ID: tID, ServiceID: sID, WorkerID: wID, State: string(models.TaskStateRunning), CreatedAt: now, UpdatedAt: now,
			})
		})
		if err != nil {
			b.Fatalf("transactional write failed: %v", err)
		}
	}
}
