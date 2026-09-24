package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func newClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Manage and inspect the CloudX cluster",
		Long:  `Manage cluster initialization, view overall cluster health, and list member nodes.`,
	}

	cmd.AddCommand(newClusterInitCmd())
	cmd.AddCommand(newClusterStatusCmd())
	cmd.AddCommand(newClusterNodesCmd())
	return cmd
}

func newClusterInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new CloudX cluster storage and configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			// 1. Create storage directory
			if err := os.MkdirAll(cfg.Storage.Path, 0755); err != nil {
				return fmt.Errorf("failed to create storage directory %s: %w", cfg.Storage.Path, err)
			}

			// 2. Initialize database
			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to initialize SQLite database at %s: %w", dbPath, err)
			}
			defer store.Close()

			// 3. Register initial node
			now := time.Now().UTC()
			node := &models.Node{
				ID:        id.ID(cfg.Node.ID),
				Name:      cfg.Node.Name,
				Address:   cfg.ControlPlane.Address,
				Status:    "READY",
				CreatedAt: now,
				UpdatedAt: now,
			}
			_ = store.Nodes().Create(ctx, node)

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "CloudX cluster initialized successfully!\n")
			fmt.Fprintf(out, "Cluster Storage:   %s\n", cfg.Storage.Path)
			fmt.Fprintf(out, "Database Path:     %s\n", dbPath)
			fmt.Fprintf(out, "Primary Node ID:   %s (%s)\n", cfg.Node.ID, cfg.Node.Name)
			fmt.Fprintf(out, "Control Plane:     %s\n", cfg.ControlPlane.Address)
			return nil
		},
	}
	return cmd
}

func newClusterStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Display CloudX cluster health and summary statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()

			// Try dialing Control Plane via gRPC with a very short timeout
			dialCtx, dialCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			conn, err := grpc.DialContext(dialCtx, cfg.ControlPlane.Address,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
			dialCancel()

			if err == nil {
				defer conn.Close()
				rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer rpcCancel()

				client := v1.NewControlPlaneServiceClient(conn)
				workersResp, err := client.ListWorkers(rpcCtx, &v1.ListWorkersRequest{})
				if err == nil {
					totalWorkers := len(workersResp.Workers)
					readyWorkers := 0
					for _, w := range workersResp.Workers {
						if strings.ToUpper(w.Status) == "READY" {
							readyWorkers++
						}
					}
					fmt.Fprintf(out, "Cluster Status:      ONLINE (Control Plane reachable)\n")
					fmt.Fprintf(out, "Control Plane:       %s\n", cfg.ControlPlane.Address)
					fmt.Fprintf(out, "Active Workers:      %d / %d READY\n", readyWorkers, totalWorkers)
					return nil
				}
			}

			// Fallback: Read directly from local state store if control plane daemon is offline
			dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dbCancel()

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			store, err := sqlite.Open(dbCtx, dbPath)
			if err != nil {
				return fmt.Errorf("control plane unreachable and failed to open local database: %w", err)
			}
			defer store.Close()

			workers, _ := store.Workers().List(dbCtx)
			nodes, _ := store.Nodes().List(dbCtx)

			fmt.Fprintf(out, "Cluster Status:      STANDBY (Local State Store)\n")
			fmt.Fprintf(out, "Registered Nodes:    %d\n", len(nodes))
			fmt.Fprintf(out, "Registered Workers:  %d\n", len(workers))
			return nil
		},
	}
	return cmd
}

func newClusterNodesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: "List all nodes in the CloudX cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			w := tabwriter.NewWriter(out, 0, 8, 3, ' ', 0)
			fmt.Fprintln(w, "NODE\tSTATUS\tADDRESS\tUPDATED")

			// Try gRPC first with short timeout
			dialCtx, dialCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			conn, err := grpc.DialContext(dialCtx, cfg.ControlPlane.Address,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
			dialCancel()

			if err == nil {
				defer conn.Close()
				rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer rpcCancel()

				client := v1.NewControlPlaneServiceClient(conn)
				listResp, err := client.ListWorkers(rpcCtx, &v1.ListWorkersRequest{})
				if err == nil && len(listResp.Workers) > 0 {
					for _, wrk := range listResp.Workers {
						nodeName := wrk.NodeId
						if nodeName == "" {
							nodeName = wrk.Id
						}
						updatedTime := time.Unix(wrk.LastHeartbeat, 0).Format("15:04:05")
						if wrk.LastHeartbeat == 0 {
							updatedTime = "N/A"
						}
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", nodeName, wrk.Status, wrk.Address, updatedTime)
					}
					return w.Flush()
				}
			}

			// Fallback to local store
			dbCtx, dbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer dbCancel()

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			store, err := sqlite.Open(dbCtx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to query nodes: %w", err)
			}
			defer store.Close()

			nodes, err := store.Nodes().List(dbCtx)
			if err != nil {
				return err
			}

			for _, n := range nodes {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", n.Name, n.Status, n.Address, n.UpdatedAt.Format("15:04:05"))
			}

			return w.Flush()
		},
	}
	return cmd
}
