package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/common/version"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/worker"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func newWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Manage and inspect CloudX workers",
		Long:  `Control worker daemon startup, join remote clusters, and query worker status.`,
	}

	cmd.AddCommand(newWorkerStartCmd())
	cmd.AddCommand(newWorkerJoinCmd())
	cmd.AddCommand(newWorkerStatusCmd())
	return cmd
}

func newWorkerStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the local CloudX worker daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load worker configuration: %w", err)
			}

			logger := logging.New(logging.ParseLevel(cfg.Logging.Level), logging.FormatText)
			logger.Info("Starting cloudx worker %s on %s...", version.Get().Version, cfg.Worker.Address)

			daemon, err := worker.NewDaemon(worker.Options{
				Config: cfg,
				Logger: logger,
			})
			if err != nil {
				return fmt.Errorf("failed to initialize worker daemon: %w", err)
			}

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			go func() {
				sig := <-sigChan
				logger.Info("Received signal %s, initiating graceful shutdown...", sig)
				cancel()
			}()

			if err := daemon.Start(ctx); err != nil {
				return fmt.Errorf("worker daemon error: %w", err)
			}

			<-ctx.Done()
			return daemon.Stop(context.Background())
		},
	}
	return cmd
}

func newWorkerJoinCmd() *cobra.Command {
	var controlPlaneAddr string

	cmd := &cobra.Command{
		Use:   "join [CONTROL_PLANE_ADDRESS]",
		Short: "Join an existing CloudX control plane cluster",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				controlPlaneAddr = args[0]
			}
			if controlPlaneAddr != "" {
				cliOpts.ControlPlaneAddr = controlPlaneAddr
			}

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Joining CloudX Control Plane at %s...\n", cfg.ControlPlane.Address)

			logger := logging.New(logging.ParseLevel(cfg.Logging.Level), logging.FormatText)
			daemon, err := worker.NewDaemon(worker.Options{
				Config: cfg,
				Logger: logger,
			})
			if err != nil {
				return fmt.Errorf("failed to initialize worker: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := daemon.Start(ctx); err != nil {
				return fmt.Errorf("failed to join cluster: %w", err)
			}

			fmt.Fprintf(out, "Successfully joined cluster %s! Worker ID: %s (Status: READY)\n", daemon.ClusterID(), daemon.ID())
			return daemon.Stop(context.Background())
		},
	}

	cmd.Flags().StringVar(&controlPlaneAddr, "control-plane", "", "Target control plane address (host:port)")
	return cmd
}

func newWorkerStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Display status and resource telemetry of cluster workers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			conn, err := grpc.DialContext(ctx, cfg.ControlPlane.Address,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
			if err != nil {
				return fmt.Errorf("failed to connect to control plane at %s: %w", cfg.ControlPlane.Address, err)
			}
			defer conn.Close()

			client := v1.NewControlPlaneServiceClient(conn)
			resp, err := client.ListWorkers(ctx, &v1.ListWorkersRequest{})
			if err != nil {
				return fmt.Errorf("failed to retrieve worker list: %w", err)
			}

			out := cmd.OutOrStdout()
			w := tabwriter.NewWriter(out, 0, 8, 3, ' ', 0)
			fmt.Fprintln(w, "NODE\tSTATUS\tADDRESS\tLAST_HEARTBEAT")

			if len(resp.Workers) == 0 {
				fmt.Fprintln(out, "No workers registered in cluster.")
				return nil
			}

			for _, wrk := range resp.Workers {
				nodeName := wrk.NodeId
				if nodeName == "" {
					nodeName = wrk.Id
				}
				hbStr := "N/A"
				if wrk.LastHeartbeat > 0 {
					elapsed := time.Since(time.Unix(wrk.LastHeartbeat, 0))
					hbStr = fmt.Sprintf("%s ago", elapsed.Round(time.Second))
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", nodeName, strings.ToUpper(wrk.Status), wrk.Address, hbStr)
			}

			return w.Flush()
		},
	}
	return cmd
}
