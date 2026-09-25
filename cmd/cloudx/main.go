package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/common/version"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

var cliOpts config.CLIOptions

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cloudx",
		Short: "CloudX is a local-first private cloud runtime & developer infrastructure platform",
		Long: `CloudX provides compute, service, and job orchestration across one or more machines
with desired-state reconciliation, deterministic scheduling, and self-healing.`,
	}

	// Global Persistent Flags for Configuration Overrides
	cmd.PersistentFlags().StringVarP(&cliOpts.ConfigPath, "config", "c", "", "Path to CloudX YAML configuration file")
	cmd.PersistentFlags().StringVar(&cliOpts.NodeID, "node-id", "", "Override Node ID")
	cmd.PersistentFlags().StringVar(&cliOpts.NodeName, "node-name", "", "Override Node Name")
	cmd.PersistentFlags().StringVar(&cliOpts.ControlPlaneAddr, "control-plane-addr", "", "Override Control Plane address (host:port)")
	cmd.PersistentFlags().StringVar(&cliOpts.StoragePath, "storage-path", "", "Override Storage persistence path")
	cmd.PersistentFlags().StringVar(&cliOpts.LogLevel, "log-level", "", "Override Logging level (debug, info, warn, error)")

	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newServerCmd())
	cmd.AddCommand(newClusterCmd())
	cmd.AddCommand(newClusterStatusCmd())
	cmd.AddCommand(newWorkerCmd())
	cmd.AddCommand(newDeployCmd())
	cmd.AddCommand(newDeploymentCmd())
	cmd.AddCommand(newRollbackCmd())
	cmd.AddCommand(newEventsCmd())
	cmd.AddCommand(newServiceCmd())
	cmd.AddCommand(newFailCmd())
	return cmd
}

func newVersionCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the CloudX version and build information",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := version.Get()
			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			fmt.Fprintln(out, info.String())
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output version information in JSON format")
	return cmd
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate CloudX configuration",
	}

	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigValidateCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show loaded and resolved CloudX configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}

			fmt.Fprintf(out, "Node ID:             %s\n", cfg.Node.ID)
			fmt.Fprintf(out, "Node Name:           %s\n", cfg.Node.Name)
			fmt.Fprintf(out, "Control Plane Addr:  %s\n", cfg.ControlPlane.Address)
			fmt.Fprintf(out, "Worker Addr:         %s\n", cfg.Worker.Address)
			fmt.Fprintf(out, "Runtime Type:        %s\n", cfg.Runtime.Type)
			fmt.Fprintf(out, "Storage Path:        %s\n", cfg.Storage.Path)
			fmt.Fprintf(out, "Heartbeat Interval:  %s\n", cfg.Health.HeartbeatInterval)
			fmt.Fprintf(out, "Logging Level:       %s\n", cfg.Logging.Level)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output resolved configuration in JSON format")
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the active or specified configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Configuration is valid.\n")
			_ = cfg
			return nil
		},
	}
	return cmd
}

func newServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the CloudX Control Plane daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			logger := logging.New(logging.ParseLevel(cfg.Logging.Level), logging.FormatText)
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			dbPath := fmt.Sprintf("%s/cloudx.db", cfg.Storage.Path)
			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to initialize sqlite state store: %w", err)
			}
			defer store.Close()

			cp, err := controlplane.New(controlplane.Options{
				Config: cfg,
				Store:  store,
				Logger: logger,
			})
			if err != nil {
				return fmt.Errorf("failed to initialize control plane: %w", err)
			}

			if err := cp.Start(ctx); err != nil {
				return err
			}

			<-ctx.Done()
			return cp.Stop(context.Background())
		},
	}
	return cmd
}

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
