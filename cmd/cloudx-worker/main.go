package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cloudx-org/cloudx/internal/common/version"
	"github.com/cloudx-org/cloudx/internal/config"
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

	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newWorkerConfigCmd())
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
