package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/simulation"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

func newFailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fail",
		Short: "Simulate deterministic cluster and workload failure scenarios",
		Long: `Deterministic fault-injection tooling for testing CloudX self-healing and recovery mechanisms.
Target CloudX-managed tasks, worker nodes, and health checks safely without risking system-level destabilization.`,
	}

	cmd.AddCommand(newFailKillProcessCmd())
	cmd.AddCommand(newFailStopWorkerCmd())
	cmd.AddCommand(newFailBreakHealthCmd())
	cmd.AddCommand(newFailDelayHeartbeatCmd())
	cmd.AddCommand(newFailExhaustResourcesCmd())
	return cmd
}

func newFailKillProcessCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "kill-process <task-id>",
		Short: "Simulate abrupt process crash by sending SIGKILL to a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := id.ID(args[0])
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to state store: %w", err)
			}
			defer store.Close()

			sim := simulation.NewSimulator(store, nil, nil, logging.NewDefaultLogger())
			res, err := sim.KillProcess(ctx, taskID)
			if err != nil {
				return fmt.Errorf("simulation failed: %w", err)
			}

			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, " [SIMULATION SUCCESS] Scenario: %s\n", res.Scenario)
			fmt.Fprintf(out, "   Target Task:  %s\n", res.TargetID)
			fmt.Fprintf(out, "   Action:       %s\n", res.Action)
			fmt.Fprintf(out, "   Timestamp:    %s\n", res.Timestamp.Format(time.RFC3339))
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newFailStopWorkerCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "stop-worker <worker-id>",
		Short: "Simulate worker node failure / stoppage",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workerID := id.ID(args[0])
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to state store: %w", err)
			}
			defer store.Close()

			sim := simulation.NewSimulator(store, nil, nil, logging.NewDefaultLogger())
			res, err := sim.StopWorker(ctx, workerID, nil)
			if err != nil {
				return fmt.Errorf("simulation failed: %w", err)
			}

			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, " [SIMULATION SUCCESS] Scenario: %s\n", res.Scenario)
			fmt.Fprintf(out, "   Target Worker: %s\n", res.TargetID)
			fmt.Fprintf(out, "   Action:        %s\n", res.Action)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newFailBreakHealthCmd() *cobra.Command {
	var (
		jsonOutput bool
		reason     string
	)

	cmd := &cobra.Command{
		Use:   "break-health <task-id>",
		Short: "Simulate health probe failure on a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := id.ID(args[0])
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to state store: %w", err)
			}
			defer store.Close()

			sim := simulation.NewSimulator(store, nil, nil, logging.NewDefaultLogger())
			res, err := sim.BreakHealthEndpoint(ctx, taskID, reason)
			if err != nil {
				return fmt.Errorf("simulation failed: %w", err)
			}

			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, " [SIMULATION SUCCESS] Scenario: %s\n", res.Scenario)
			fmt.Fprintf(out, "   Target Task: %s\n", res.TargetID)
			fmt.Fprintf(out, "   Action:      %s\n", res.Action)
			return nil
		},
	}

	cmd.Flags().StringVarP(&reason, "reason", "r", "Simulated 503 Service Unavailable", "Failure error message")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newFailDelayHeartbeatCmd() *cobra.Command {
	var (
		jsonOutput bool
		delay      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "delay-heartbeat <worker-id>",
		Short: "Simulate heartbeat timeout / partition on a worker node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workerID := id.ID(args[0])
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to state store: %w", err)
			}
			defer store.Close()

			sim := simulation.NewSimulator(store, nil, nil, logging.NewDefaultLogger())
			res, err := sim.DelayHeartbeat(ctx, workerID, delay)
			if err != nil {
				return fmt.Errorf("simulation failed: %w", err)
			}

			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, " [SIMULATION SUCCESS] Scenario: %s\n", res.Scenario)
			fmt.Fprintf(out, "   Target Worker: %s\n", res.TargetID)
			fmt.Fprintf(out, "   Action:        %s\n", res.Action)
			return nil
		},
	}

	cmd.Flags().DurationVarP(&delay, "delay", "d", 45*time.Second, "Duration to backdate heartbeat")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newFailExhaustResourcesCmd() *cobra.Command {
	var (
		jsonOutput bool
		mb         int
	)

	cmd := &cobra.Command{
		Use:   "exhaust-resources <task-id>",
		Short: "Simulate bounded memory allocation / resource pressure on a task sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID := id.ID(args[0])
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to state store: %w", err)
			}
			defer store.Close()

			sim := simulation.NewSimulator(store, nil, nil, logging.NewDefaultLogger())
			res, err := sim.ExhaustResources(ctx, taskID, mb)
			if err != nil {
				return fmt.Errorf("simulation failed: %w", err)
			}

			out := cmd.OutOrStdout()
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, " [SIMULATION SUCCESS] Scenario: %s\n", res.Scenario)
			fmt.Fprintf(out, "   Target Task: %s\n", res.TargetID)
			fmt.Fprintf(out, "   Action:      %s\n", res.Action)
			return nil
		},
	}

	cmd.Flags().IntVarP(&mb, "memory-mb", "m", 64, "Safe memory allocation amount in MB")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}
