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

// --- Node Repository ---
type nodeRepo struct{ exec dbExecutor }

func (r *nodeRepo) Create(ctx context.Context, n *models.Node) error {
	query := `INSERT INTO nodes (id, name, address, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, n.ID.String(), n.Name, n.Address, n.Status, n.CreatedAt, n.UpdatedAt)
	return err
}

func (r *nodeRepo) Get(ctx context.Context, idVal id.ID) (*models.Node, error) {
	query := `SELECT id, name, address, status, created_at, updated_at FROM nodes WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	var n models.Node
	var idStr string
	if err := row.Scan(&idStr, &n.Name, &n.Address, &n.Status, &n.CreatedAt, &n.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("node %s not found: %w", idVal, err)
		}
		return nil, err
	}
	n.ID = id.ID(idStr)
	return &n, nil
}

func (r *nodeRepo) List(ctx context.Context) ([]*models.Node, error) {
	query := `SELECT id, name, address, status, created_at, updated_at FROM nodes ORDER BY created_at ASC`
	rows, err := r.exec.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*models.Node
	for rows.Next() {
		var n models.Node
		var idStr string
		if err := rows.Scan(&idStr, &n.Name, &n.Address, &n.Status, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.ID = id.ID(idStr)
		nodes = append(nodes, &n)
	}
	return nodes, nil
}

func (r *nodeRepo) Update(ctx context.Context, n *models.Node) error {
	n.UpdatedAt = time.Now().UTC()
	query := `UPDATE nodes SET name = ?, address = ?, status = ?, updated_at = ? WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, n.Name, n.Address, n.Status, n.UpdatedAt, n.ID.String())
	return err
}

func (r *nodeRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM nodes WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

// --- Worker Repository ---
type workerRepo struct{ exec dbExecutor }

func (r *workerRepo) Create(ctx context.Context, w *models.Worker) error {
	query := `INSERT INTO workers (id, node_id, address, status, heartbeat, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, w.ID.String(), w.NodeID.String(), w.Address, w.Status, w.Heartbeat, w.CreatedAt, w.UpdatedAt)
	return err
}

func (r *workerRepo) Get(ctx context.Context, idVal id.ID) (*models.Worker, error) {
	query := `SELECT id, node_id, address, status, heartbeat, created_at, updated_at FROM workers WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	var w models.Worker
	var idStr, nodeIDStr string
	if err := row.Scan(&idStr, &nodeIDStr, &w.Address, &w.Status, &w.Heartbeat, &w.CreatedAt, &w.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("worker %s not found: %w", idVal, err)
		}
		return nil, err
	}
	w.ID = id.ID(idStr)
	w.NodeID = id.ID(nodeIDStr)
	return &w, nil
}

func (r *workerRepo) List(ctx context.Context) ([]*models.Worker, error) {
	query := `SELECT id, node_id, address, status, heartbeat, created_at, updated_at FROM workers ORDER BY created_at ASC`
	rows, err := r.exec.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []*models.Worker
	for rows.Next() {
		var w models.Worker
		var idStr, nodeIDStr string
		if err := rows.Scan(&idStr, &nodeIDStr, &w.Address, &w.Status, &w.Heartbeat, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		w.ID = id.ID(idStr)
		w.NodeID = id.ID(nodeIDStr)
		workers = append(workers, &w)
	}
	return workers, nil
}

func (r *workerRepo) Update(ctx context.Context, w *models.Worker) error {
	w.UpdatedAt = time.Now().UTC()
	query := `UPDATE workers SET node_id = ?, address = ?, status = ?, heartbeat = ?, updated_at = ? WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, w.NodeID.String(), w.Address, w.Status, w.Heartbeat, w.UpdatedAt, w.ID.String())
	return err
}

func (r *workerRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM workers WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

// --- Service Repository ---
type serviceRepo struct{ exec dbExecutor }

func (r *serviceRepo) Create(ctx context.Context, s *models.Service) error {
	query := `INSERT INTO services (id, name, replicas, runtime, command, status, spec_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, s.ID.String(), s.Name, s.Replicas, s.Runtime, s.Command, s.Status, s.SpecJSON, s.CreatedAt, s.UpdatedAt)
	return err
}

func (r *serviceRepo) Get(ctx context.Context, idVal id.ID) (*models.Service, error) {
	query := `SELECT id, name, replicas, runtime, command, status, spec_json, created_at, updated_at FROM services WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	return scanService(row)
}

func (r *serviceRepo) GetByName(ctx context.Context, name string) (*models.Service, error) {
	query := `SELECT id, name, replicas, runtime, command, status, spec_json, created_at, updated_at FROM services WHERE name = ?`
	row := r.exec.QueryRowContext(ctx, query, name)
	return scanService(row)
}

func scanService(row *sql.Row) (*models.Service, error) {
	var s models.Service
	var idStr string
	if err := row.Scan(&idStr, &s.Name, &s.Replicas, &s.Runtime, &s.Command, &s.Status, &s.SpecJSON, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	s.ID = id.ID(idStr)
	return &s, nil
}

func (r *serviceRepo) List(ctx context.Context) ([]*models.Service, error) {
	query := `SELECT id, name, replicas, runtime, command, status, spec_json, created_at, updated_at FROM services ORDER BY created_at ASC`
	rows, err := r.exec.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var services []*models.Service
	for rows.Next() {
		var s models.Service
		var idStr string
		if err := rows.Scan(&idStr, &s.Name, &s.Replicas, &s.Runtime, &s.Command, &s.Status, &s.SpecJSON, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.ID = id.ID(idStr)
		services = append(services, &s)
	}
	return services, nil
}

func (r *serviceRepo) Update(ctx context.Context, s *models.Service) error {
	s.UpdatedAt = time.Now().UTC()
	query := `UPDATE services SET name = ?, replicas = ?, runtime = ?, command = ?, status = ?, spec_json = ?, updated_at = ? WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, s.Name, s.Replicas, s.Runtime, s.Command, s.Status, s.SpecJSON, s.UpdatedAt, s.ID.String())
	return err
}

func (r *serviceRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM services WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

// --- Deployment Repository ---
type deploymentRepo struct{ exec dbExecutor }

func (r *deploymentRepo) Create(ctx context.Context, d *models.Deployment) error {
	query := `INSERT INTO deployments (id, service_id, version, status, spec_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, d.ID.String(), d.ServiceID.String(), d.Version, d.Status, d.SpecJSON, d.CreatedAt, d.UpdatedAt)
	return err
}

func (r *deploymentRepo) Get(ctx context.Context, idVal id.ID) (*models.Deployment, error) {
	query := `SELECT id, service_id, version, status, spec_json, created_at, updated_at FROM deployments WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	var d models.Deployment
	var idStr, srvIDStr string
	if err := row.Scan(&idStr, &srvIDStr, &d.Version, &d.Status, &d.SpecJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.ID = id.ID(idStr)
	d.ServiceID = id.ID(srvIDStr)
	return &d, nil
}

func (r *deploymentRepo) ListByService(ctx context.Context, serviceID id.ID) ([]*models.Deployment, error) {
	query := `SELECT id, service_id, version, status, spec_json, created_at, updated_at FROM deployments WHERE service_id = ? ORDER BY created_at DESC`
	rows, err := r.exec.QueryContext(ctx, query, serviceID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deps []*models.Deployment
	for rows.Next() {
		var d models.Deployment
		var idStr, srvIDStr string
		if err := rows.Scan(&idStr, &srvIDStr, &d.Version, &d.Status, &d.SpecJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.ID = id.ID(idStr)
		d.ServiceID = id.ID(srvIDStr)
		deps = append(deps, &d)
	}
	return deps, nil
}

func (r *deploymentRepo) Update(ctx context.Context, d *models.Deployment) error {
	d.UpdatedAt = time.Now().UTC()
	query := `UPDATE deployments SET version = ?, status = ?, spec_json = ?, updated_at = ? WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, d.Version, d.Status, d.SpecJSON, d.UpdatedAt, d.ID.String())
	return err
}

func (r *deploymentRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM deployments WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

// --- Task Repository ---
type taskRepo struct{ exec dbExecutor }

func (r *taskRepo) Create(ctx context.Context, t *models.Task) error {
	query := `INSERT INTO tasks (id, service_id, job_id, deployment_id, worker_id, state, pid, exit_code, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, t.ID.String(), t.ServiceID.String(), t.JobID.String(), t.DeploymentID.String(), t.WorkerID.String(), t.State, t.PID, t.ExitCode, t.CreatedAt, t.UpdatedAt)
	return err
}

func (r *taskRepo) Get(ctx context.Context, idVal id.ID) (*models.Task, error) {
	query := `SELECT id, service_id, job_id, deployment_id, worker_id, state, pid, exit_code, created_at, updated_at FROM tasks WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	return scanTask(row)
}

func scanTask(row *sql.Row) (*models.Task, error) {
	var t models.Task
	var idStr, srvIDStr, jobIDStr, depIDStr, wrkIDStr string
	if err := row.Scan(&idStr, &srvIDStr, &jobIDStr, &depIDStr, &wrkIDStr, &t.State, &t.PID, &t.ExitCode, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.ID = id.ID(idStr)
	t.ServiceID = id.ID(srvIDStr)
	t.JobID = id.ID(jobIDStr)
	t.DeploymentID = id.ID(depIDStr)
	t.WorkerID = id.ID(wrkIDStr)
	return &t, nil
}

func (r *taskRepo) List(ctx context.Context) ([]*models.Task, error) {
	query := `SELECT id, service_id, job_id, deployment_id, worker_id, state, pid, exit_code, created_at, updated_at FROM tasks ORDER BY created_at ASC`
	return r.queryTasks(ctx, query)
}

func (r *taskRepo) ListByService(ctx context.Context, serviceID id.ID) ([]*models.Task, error) {
	query := `SELECT id, service_id, job_id, deployment_id, worker_id, state, pid, exit_code, created_at, updated_at FROM tasks WHERE service_id = ? ORDER BY created_at ASC`
	return r.queryTasks(ctx, query, serviceID.String())
}

func (r *taskRepo) ListByWorker(ctx context.Context, workerID id.ID) ([]*models.Task, error) {
	query := `SELECT id, service_id, job_id, deployment_id, worker_id, state, pid, exit_code, created_at, updated_at FROM tasks WHERE worker_id = ? ORDER BY created_at ASC`
	return r.queryTasks(ctx, query, workerID.String())
}

func (r *taskRepo) queryTasks(ctx context.Context, query string, args ...any) ([]*models.Task, error) {
	rows, err := r.exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*models.Task
	for rows.Next() {
		var t models.Task
		var idStr, srvIDStr, jobIDStr, depIDStr, wrkIDStr string
		if err := rows.Scan(&idStr, &srvIDStr, &jobIDStr, &depIDStr, &wrkIDStr, &t.State, &t.PID, &t.ExitCode, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.ID = id.ID(idStr)
		t.ServiceID = id.ID(srvIDStr)
		t.JobID = id.ID(jobIDStr)
		t.DeploymentID = id.ID(depIDStr)
		t.WorkerID = id.ID(wrkIDStr)
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

func (r *taskRepo) Update(ctx context.Context, t *models.Task) error {
	t.UpdatedAt = time.Now().UTC()
	query := `UPDATE tasks SET service_id = ?, job_id = ?, deployment_id = ?, worker_id = ?, state = ?, pid = ?, exit_code = ?, updated_at = ? WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, t.ServiceID.String(), t.JobID.String(), t.DeploymentID.String(), t.WorkerID.String(), t.State, t.PID, t.ExitCode, t.UpdatedAt, t.ID.String())
	return err
}

func (r *taskRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM tasks WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

// --- Job Repository ---
type jobRepo struct{ exec dbExecutor }

func (r *jobRepo) Create(ctx context.Context, j *models.Job) error {
	specJSON := j.SpecJSON
	if specJSON == "" {
		specJSON = "{}"
	}
	query := `INSERT INTO jobs (id, name, command, status, spec_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, j.ID.String(), j.Name, j.Command, j.Status, specJSON, j.CreatedAt, j.UpdatedAt)
	return err
}

func (r *jobRepo) Get(ctx context.Context, idVal id.ID) (*models.Job, error) {
	query := `SELECT id, name, command, status, spec_json, created_at, updated_at FROM jobs WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	var j models.Job
	var idStr string
	if err := row.Scan(&idStr, &j.Name, &j.Command, &j.Status, &j.SpecJSON, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return nil, err
	}
	j.ID = id.ID(idStr)
	return &j, nil
}

func (r *jobRepo) List(ctx context.Context) ([]*models.Job, error) {
	query := `SELECT id, name, command, status, spec_json, created_at, updated_at FROM jobs ORDER BY created_at ASC`
	rows, err := r.exec.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*models.Job
	for rows.Next() {
		var j models.Job
		var idStr string
		if err := rows.Scan(&idStr, &j.Name, &j.Command, &j.Status, &j.SpecJSON, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.ID = id.ID(idStr)
		jobs = append(jobs, &j)
	}
	return jobs, nil
}

func (r *jobRepo) Update(ctx context.Context, j *models.Job) error {
	j.UpdatedAt = time.Now().UTC()
	specJSON := j.SpecJSON
	if specJSON == "" {
		specJSON = "{}"
	}
	query := `UPDATE jobs SET name = ?, command = ?, status = ?, spec_json = ?, updated_at = ? WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, j.Name, j.Command, j.Status, specJSON, j.UpdatedAt, j.ID.String())
	return err
}

func (r *jobRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM jobs WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

// --- Volume Repository ---
type volumeRepo struct{ exec dbExecutor }

func (r *volumeRepo) Create(ctx context.Context, v *models.Volume) error {
	specJSON := v.SpecJSON
	if specJSON == "" {
		specJSON = "{}"
	}
	query := `INSERT INTO volumes (id, name, worker_id, path, driver, spec_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, v.ID.String(), v.Name, v.WorkerID.String(), v.Path, v.Driver, specJSON, v.CreatedAt, v.UpdatedAt)
	return err
}

func (r *volumeRepo) Get(ctx context.Context, idVal id.ID) (*models.Volume, error) {
	query := `SELECT id, name, worker_id, path, driver, spec_json, created_at, updated_at FROM volumes WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	var v models.Volume
	var idStr, wrkIDStr string
	if err := row.Scan(&idStr, &v.Name, &wrkIDStr, &v.Path, &v.Driver, &v.SpecJSON, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	v.ID = id.ID(idStr)
	v.WorkerID = id.ID(wrkIDStr)
	return &v, nil
}

func (r *volumeRepo) List(ctx context.Context) ([]*models.Volume, error) {
	query := `SELECT id, name, worker_id, path, driver, spec_json, created_at, updated_at FROM volumes ORDER BY created_at ASC`
	rows, err := r.exec.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var volumes []*models.Volume
	for rows.Next() {
		var v models.Volume
		var idStr, wrkIDStr string
		if err := rows.Scan(&idStr, &v.Name, &wrkIDStr, &v.Path, &v.Driver, &v.SpecJSON, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.ID = id.ID(idStr)
		v.WorkerID = id.ID(wrkIDStr)
		volumes = append(volumes, &v)
	}
	return volumes, nil
}

func (r *volumeRepo) Update(ctx context.Context, v *models.Volume) error {
	v.UpdatedAt = time.Now().UTC()
	specJSON := v.SpecJSON
	if specJSON == "" {
		specJSON = "{}"
	}
	query := `UPDATE volumes SET name = ?, worker_id = ?, path = ?, driver = ?, spec_json = ?, updated_at = ? WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, v.Name, v.WorkerID.String(), v.Path, v.Driver, specJSON, v.UpdatedAt, v.ID.String())
	return err
}

func (r *volumeRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM volumes WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

// --- Network Repository ---
type networkRepo struct{ exec dbExecutor }

func (r *networkRepo) Create(ctx context.Context, n *models.Network) error {
	query := `INSERT INTO networks (id, name, subnet, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, n.ID.String(), n.Name, n.Subnet, n.CreatedAt, n.UpdatedAt)
	return err
}

func (r *networkRepo) Get(ctx context.Context, idVal id.ID) (*models.Network, error) {
	query := `SELECT id, name, subnet, created_at, updated_at FROM networks WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	var n models.Network
	var idStr string
	if err := row.Scan(&idStr, &n.Name, &n.Subnet, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return nil, err
	}
	n.ID = id.ID(idStr)
	return &n, nil
}

func (r *networkRepo) List(ctx context.Context) ([]*models.Network, error) {
	query := `SELECT id, name, subnet, created_at, updated_at FROM networks ORDER BY created_at ASC`
	rows, err := r.exec.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nets []*models.Network
	for rows.Next() {
		var n models.Network
		var idStr string
		if err := rows.Scan(&idStr, &n.Name, &n.Subnet, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.ID = id.ID(idStr)
		nets = append(nets, &n)
	}
	return nets, nil
}

func (r *networkRepo) Update(ctx context.Context, n *models.Network) error {
	n.UpdatedAt = time.Now().UTC()
	query := `UPDATE networks SET name = ?, subnet = ?, updated_at = ? WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, n.Name, n.Subnet, n.UpdatedAt, n.ID.String())
	return err
}

func (r *networkRepo) Delete(ctx context.Context, idVal id.ID) error {
	query := `DELETE FROM networks WHERE id = ?`
	_, err := r.exec.ExecContext(ctx, query, idVal.String())
	return err
}

// --- Event Repository ---
type eventRepo struct{ exec dbExecutor }

func (r *eventRepo) Append(ctx context.Context, e *models.Event) error {
	query := `INSERT INTO events (id, type, source, entity_id, payload, created_at) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := r.exec.ExecContext(ctx, query, e.ID.String(), e.Type, e.Source, e.EntityID.String(), e.Payload, e.CreatedAt)
	return err
}

func (r *eventRepo) Get(ctx context.Context, idVal id.ID) (*models.Event, error) {
	query := `SELECT id, type, source, entity_id, payload, created_at FROM events WHERE id = ?`
	row := r.exec.QueryRowContext(ctx, query, idVal.String())
	var e models.Event
	var idStr, entityIDStr string
	if err := row.Scan(&idStr, &e.Type, &e.Source, &entityIDStr, &e.Payload, &e.CreatedAt); err != nil {
		return nil, err
	}
	e.ID = id.ID(idStr)
	e.EntityID = id.ID(entityIDStr)
	return &e, nil
}

func (r *eventRepo) List(ctx context.Context, limit int) ([]*models.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT id, type, source, entity_id, payload, created_at FROM events ORDER BY created_at DESC LIMIT ?`
	rows, err := r.exec.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.Event
	for rows.Next() {
		var e models.Event
		var idStr, entityIDStr string
		if err := rows.Scan(&idStr, &e.Type, &e.Source, &entityIDStr, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.ID = id.ID(idStr)
		e.EntityID = id.ID(entityIDStr)
		events = append(events, &e)
	}
	return events, nil
}

func (r *eventRepo) ListByEntity(ctx context.Context, entityID id.ID) ([]*models.Event, error) {
	query := `SELECT id, type, source, entity_id, payload, created_at FROM events WHERE entity_id = ? ORDER BY created_at ASC`
	rows, err := r.exec.QueryContext(ctx, query, entityID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*models.Event
	for rows.Next() {
		var e models.Event
		var idStr, entityIDStr string
		if err := rows.Scan(&idStr, &e.Type, &e.Source, &entityIDStr, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.ID = id.ID(idStr)
		e.EntityID = id.ID(entityIDStr)
		events = append(events, &e)
	}
	return events, nil
}

// compile-time interface assertions
var (
	_ state.NodeRepository       = (*nodeRepo)(nil)
	_ state.WorkerRepository     = (*workerRepo)(nil)
	_ state.ServiceRepository    = (*serviceRepo)(nil)
	_ state.DeploymentRepository = (*deploymentRepo)(nil)
	_ state.TaskRepository       = (*taskRepo)(nil)
	_ state.JobRepository        = (*jobRepo)(nil)
	_ state.VolumeRepository     = (*volumeRepo)(nil)
	_ state.NetworkRepository    = (*networkRepo)(nil)
	_ state.EventRepository      = (*eventRepo)(nil)
)
