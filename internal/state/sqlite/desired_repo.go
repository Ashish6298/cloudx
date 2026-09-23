package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/models"
)

// --- Desired State Repository ---
type desiredStateRepo struct{ exec dbExecutor }

func (r *desiredStateRepo) Create(ctx context.Context, s *models.ServiceDesiredState) error {
	if err := s.Validate(); err != nil {
		return err
	}
	specJSON, err := s.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize desired state to JSON: %w", err)
	}

	query := `INSERT INTO desired_states (id, name, version, replicas, runtime, command, spec_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = r.exec.ExecContext(ctx, query, s.ID.String(), s.Name, s.Version, s.Replicas, s.Runtime, s.Command, specJSON, s.CreatedAt, s.UpdatedAt)
	return err
}

func (r *desiredStateRepo) Get(ctx context.Context, idVal id.ID) (*models.ServiceDesiredState, error) {
	query := `SELECT id, name, version, replicas, runtime, command, spec_json, created_at, updated_at FROM desired_states WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	return scanDesiredState(row)
}

func (r *desiredStateRepo) GetByName(ctx context.Context, name string) (*models.ServiceDesiredState, error) {
	query := `SELECT id, name, version, replicas, runtime, command, spec_json, created_at, updated_at FROM desired_states WHERE name = ?`
	row := r.exec.QueryRowContext(ctx, query, name)
	return scanDesiredState(row)
}

func scanDesiredState(row *sql.Row) (*models.ServiceDesiredState, error) {
	var idStr, name, version, runtime, command, specJSON string
	var replicas int
	var createdAt, updatedAt time.Time

	if err := row.Scan(&idStr, &name, &version, &replicas, &runtime, &command, &specJSON, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("desired state not found: %w", err)
		}
		return nil, err
	}

	desired, err := models.FromJSON(specJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to parse desired state spec_json: %w", err)
	}
	desired.ID = id.ID(idStr)
	desired.Name = name
	desired.Version = version
	desired.Replicas = replicas
	desired.Runtime = runtime
	desired.Command = command
	desired.CreatedAt = createdAt
	desired.UpdatedAt = updatedAt
	return desired, nil
}

func (r *desiredStateRepo) List(ctx context.Context) ([]*models.ServiceDesiredState, error) {
	query := `SELECT id, name, version, replicas, runtime, command, spec_json, created_at, updated_at FROM desired_states ORDER BY created_at ASC`
	rows, err := r.exec.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*models.ServiceDesiredState
	for rows.Next() {
		var idStr, name, version, runtime, command, specJSON string
		var replicas int
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&idStr, &name, &version, &replicas, &runtime, &command, &specJSON, &createdAt, &updatedAt); err != nil {
			return nil, err
		}

		desired, err := models.FromJSON(specJSON)
		if err != nil {
			return nil, err
		}
		desired.ID = id.ID(idStr)
		desired.Name = name
		desired.Version = version
		desired.Replicas = replicas
		desired.Runtime = runtime
		desired.Command = command
		desired.CreatedAt = createdAt
		desired.UpdatedAt = updatedAt
		list = append(list, desired)
	}
	return list, nil
}

func (r *desiredStateRepo) Update(ctx context.Context, s *models.ServiceDesiredState) error {
	if err := s.Validate(); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC()
	specJSON, err := s.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize desired state to JSON: %w", err)
	}

	query := `UPDATE desired_states SET name = ?, version = ?, replicas = ?, runtime = ?, command = ?, spec_json = ?, updated_at = ? WHERE id = ?`
	_, err = r.exec.ExecContext(ctx, query, s.Name, s.Version, s.Replicas, s.Runtime, s.Command, specJSON, s.UpdatedAt, s.ID.String())
	return err
}

func (r *desiredStateRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM desired_states WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

var _ state.DesiredStateStore = (*desiredStateRepo)(nil)
