package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage and inspect cluster worker nodes",
		Long:  `Inspect node statuses, list cluster nodes, and drain workloads safely from workers.`,
	}

	cmd.AddCommand(newNodeListCmd())
	cmd.AddCommand(newNodeDrainCmd())
	return cmd
}

func newNodeListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all nodes in the CloudX cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			// Query local state store
			dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dbCancel()

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			store, err := sqlite.Open(dbCtx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to open cluster database: %w", err)
			}
			defer store.Close()

			nodes, _ := store.Nodes().List(dbCtx)
			workers, _ := store.Workers().List(dbCtx)
			tasks, _ := store.Tasks().List(dbCtx)

			nodeMap := make(map[id.ID]*models.Node)
			for _, n := range nodes {
				nodeMap[n.ID] = n
			}

			workerTaskCount := make(map[id.ID]int)
			for _, t := range tasks {
				if t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
					workerTaskCount[t.WorkerID]++
				}
			}

			type NodeRow struct {
				Node      string `json:"node"`
				WorkerID  string `json:"worker_id"`
				Address   string `json:"address"`
				Status    string `json:"status"`
				Tasks     int    `json:"active_tasks"`
				Heartbeat string `json:"last_heartbeat"`
			}

			var rows []NodeRow
			for _, w := range workers {
				nodeName := string(w.NodeID)
				if n, ok := nodeMap[w.NodeID]; ok && n.Name != "" {
					nodeName = n.Name
				}
				hbStr := "N/A"
				if !w.Heartbeat.IsZero() {
					hbStr = w.Heartbeat.Format("15:04:05")
				}
				rows = append(rows, NodeRow{
					Node:      nodeName,
					WorkerID:  string(w.ID),
					Address:   w.Address,
					Status:    w.Status,
					Tasks:     workerTaskCount[w.ID],
					Heartbeat: hbStr,
				})
			}

			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}

			w := tabwriter.NewWriter(out, 0, 8, 3, ' ', 0)
			fmt.Fprintln(w, "NODE\tWORKER ID\tSTATUS\tTASKS\tADDRESS\tHEARTBEAT")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", r.Node, r.WorkerID, r.Status, r.Tasks, r.Address, r.Heartbeat)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

func newNodeDrainCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "drain <NODE_OR_WORKER_ID>",
		Short: "Safely drain a worker node by evicting its active tasks and disabling new scheduling",
		Long: `Transitions the target worker node to DRAINING status:
- Disables scheduling of new workloads onto the node.
- Flags existing tasks on the node for eviction and graceful migration by the reconciler.
- Transitions the node status to EMPTY once all active tasks are decommissioned.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(args[0])
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dbCancel()

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			store, err := sqlite.Open(dbCtx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to open cluster database: %w", err)
			}
			defer store.Close()

			// Search for target worker by ID, NodeID, or Node Name
			workers, err := store.Workers().List(dbCtx)
			if err != nil {
				return fmt.Errorf("failed to list workers: %w", err)
			}
			nodes, _ := store.Nodes().List(dbCtx)

			var matchedWorker *models.Worker
			for _, w := range workers {
				if string(w.ID) == target || string(w.NodeID) == target {
					matchedWorker = w
					break
				}
			}

			if matchedWorker == nil {
				for _, n := range nodes {
					if n.Name == target || string(n.ID) == target {
						// Match worker for this node
						for _, w := range workers {
							if w.NodeID == n.ID {
								matchedWorker = w
								break
							}
						}
					}
				}
			}

			if matchedWorker == nil {
				return fmt.Errorf("node or worker %q not found in cluster", target)
			}

			now := time.Now().UTC()
			previousStatus := matchedWorker.Status
			matchedWorker.Status = "DRAINING"
			matchedWorker.UpdatedAt = now

			if err := store.Workers().Update(dbCtx, matchedWorker); err != nil {
				return fmt.Errorf("failed to update worker status to DRAINING: %w", err)
			}

			// Record audit event
			_ = store.Events().Append(dbCtx, &models.Event{
				ID:        id.NewEventID(),
				Type:      "NODE_DRAINING",
				Source:    "cli",
				EntityID:  matchedWorker.ID,
				Payload:   fmt.Sprintf(`{"node_id":"%s","previous_status":"%s","current_status":"DRAINING"}`, matchedWorker.NodeID, previousStatus),
				CreatedAt: now,
			})

			// Count active tasks currently scheduled on this worker
			tasks, _ := store.Tasks().List(dbCtx)
			activeTasks := 0
			for _, t := range tasks {
				if t.WorkerID == matchedWorker.ID &&
					t.State != string(models.TaskStateStopped) &&
					t.State != string(models.TaskStateFailed) &&
					t.State != string(models.TaskStateLost) {
					activeTasks++
				}
			}

			type DrainResult struct {
				WorkerID       string `json:"worker_id"`
				NodeID         string `json:"node_id"`
				Status         string `json:"status"`
				PreviousStatus string `json:"previous_status"`
				ActiveTasks    int    `json:"active_tasks_evicting"`
				Message        string `json:"message"`
			}

			res := DrainResult{
				WorkerID:       string(matchedWorker.ID),
				NodeID:         string(matchedWorker.NodeID),
				Status:         "DRAINING",
				PreviousStatus: previousStatus,
				ActiveTasks:    activeTasks,
				Message:        fmt.Sprintf("Worker %s is now DRAINING (%d active tasks marked for eviction). Workloads will be rescheduled to other nodes.", matchedWorker.ID, activeTasks),
			}

			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintln(out, "NODE DRAIN INITIATED")
			fmt.Fprintf(out, "Worker ID:    %s\n", matchedWorker.ID)
			fmt.Fprintf(out, "Node ID:      %s\n", matchedWorker.NodeID)
			fmt.Fprintf(out, "Status:       DRAINING (was %s)\n", previousStatus)
			fmt.Fprintf(out, "Evicting:     %d active tasks\n", activeTasks)
			fmt.Fprintf(out, "Summary:      %s\n", res.Message)

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}
