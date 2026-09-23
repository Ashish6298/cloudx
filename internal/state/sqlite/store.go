package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudx-org/cloudx/internal/state"
	_ "modernc.org/sqlite"
)

type dbExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db   *sql.DB
	exec dbExecutor
}

// Open initializes and migrates the SQLite state store.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	var dsn string
	if dbPath == "" || dbPath == ":memory:" {
		// Use shared cache URI for in-memory databases so multiple connections share the schema
		dsn = "file:cloudx_mem?mode=memory&cache=shared&_pragma=foreign_keys(ON)"
	} else {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
		dsn = fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dbPath)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Ensure SQLite handles concurrent connections gracefully
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{
		db:   db,
		exec: db,
	}, nil
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) Nodes() state.NodeRepository {
	return &nodeRepo{exec: s.exec}
}

func (s *Store) Workers() state.WorkerRepository {
	return &workerRepo{exec: s.exec}
}

func (s *Store) Services() state.ServiceRepository {
	return &serviceRepo{exec: s.exec}
}

func (s *Store) Deployments() state.DeploymentRepository {
	return &deploymentRepo{exec: s.exec}
}

func (s *Store) Tasks() state.TaskRepository {
	return &taskRepo{exec: s.exec}
}

func (s *Store) Jobs() state.JobRepository {
	return &jobRepo{exec: s.exec}
}

func (s *Store) Volumes() state.VolumeRepository {
	return &volumeRepo{exec: s.exec}
}

func (s *Store) Networks() state.NetworkRepository {
	return &networkRepo{exec: s.exec}
}

func (s *Store) Events() state.EventRepository {
	return &eventRepo{exec: s.exec}
}

func (s *Store) Transaction(ctx context.Context, fn func(tx state.Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	txStore := &Store{
		db:   s.db,
		exec: tx,
	}

	if err := fn(txStore); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}
