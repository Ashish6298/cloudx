package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/common/version"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/worker"
	"github.com/spf13/cobra"
)

var workerCLIOpts config.CLIOptions

func newWorkerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloudx-worker",
		Short: "CloudX Worker daemon for executing and monitoring machine workloads",
		Long: `cloudx-worker connects to the CloudX control plane, receives task assignments,
supervises native processes, collects metrics/logs, and performs health checks.`,
	}

	cmd.PersistentFlags().StringVarP(&workerCLIOpts.ConfigPath, "config", "c", "", "Path to CloudX YAML configuration file")
	cmd.PersistentFlags().StringVar(&workerCLIOpts.NodeID, "node-id", "", "Override Node ID")
	cmd.PersistentFlags().StringVar(&workerCLIOpts.WorkerAddr, "worker-addr", "", "Override Worker listen address (host:port)")
	cmd.PersistentFlags().StringVar(&workerCLIOpts.ControlPlaneAddr, "control-plane-addr", "", "Override Control Plane endpoint")
	cmd.PersistentFlags().StringVar(&workerCLIOpts.LogLevel, "log-level", "", "Override Logging level (debug, info, warn, error)")

	cmd.AddCommand(newStartCmd())
	cmd.AddCommand(newJoinCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newWorkerConfigCmd())
	return cmd
}

func newJoinCmd() *cobra.Command {
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
				workerCLIOpts.ControlPlaneAddr = controlPlaneAddr
			}

			cfg, err := config.Load(workerCLIOpts)
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

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Inspect local worker status and tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(workerCLIOpts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Worker Node ID:      %s\n", cfg.Node.ID)
			fmt.Fprintf(out, "Worker Listen Addr:  %s\n", cfg.Worker.Address)
			fmt.Fprintf(out, "Control Plane Addr:  %s\n", cfg.ControlPlane.Address)
			fmt.Fprintf(out, "Runtime Engine:      %s\n", cfg.Runtime.Type)
			fmt.Fprintf(out, "Heartbeat Interval:  %s\n", cfg.Health.HeartbeatInterval)
			return nil
		},
	}
	return cmd
}

func newStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the CloudX worker daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(workerCLIOpts)
			if err != nil {
				return fmt.Errorf("failed to load worker configuration: %w", err)
			}

			logger := logging.New(logging.ParseLevel(cfg.Logging.Level), logging.FormatText)
			logger.Info("Starting cloudx-worker %s...", version.Get().Version)

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

			// Block until context canceled
			<-ctx.Done()
			return daemon.Stop(context.Background())
		},
	}
	return cmd
}

func newVersionCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the CloudX Worker version and build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Get()
			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			fmt.Fprintf(out, "cloudx-worker %s\n", info.String())
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output version information in JSON format")
	return cmd
}

func newWorkerConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate Worker configuration",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show loaded Worker configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(workerCLIOpts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Worker Node ID:      %s\n", cfg.Node.ID)
			fmt.Fprintf(out, "Worker Listen Addr:  %s\n", cfg.Worker.Address)
			fmt.Fprintf(out, "Control Plane Addr:  %s\n", cfg.ControlPlane.Address)
			fmt.Fprintf(out, "Runtime Type:        %s\n", cfg.Runtime.Type)
			fmt.Fprintf(out, "Logging Level:       %s\n", cfg.Logging.Level)
			return nil
		},
	})
	return cmd
}

func main() {
	workerCmd := newWorkerCmd()
	if err := workerCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
