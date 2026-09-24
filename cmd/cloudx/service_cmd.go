package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var manifestPath string

	cmd := &cobra.Command{
		Use:   "deploy [flags] [manifest-path]",
		Short: "Deploy services defined in a YAML manifest file",
		Long: `Deploy one or more services into the CloudX cluster from a declarative YAML specification.
Flow: Config -> Validate -> Persist desired state -> Reconcile -> Schedule -> Assign -> Execute -> Monitor.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := manifestPath
			if filePath == "" && len(args) > 0 {
				filePath = args[0]
			}
			if filePath == "" {
				// Default fallback to cloudx.yaml or service.yaml in current dir
				if _, err := os.Stat("cloudx.yaml"); err == nil {
					filePath = "cloudx.yaml"
				} else if _, err := os.Stat("service.yaml"); err == nil {
					filePath = "service.yaml"
				} else {
					return fmt.Errorf("manifest file required: please specify via --file or argument")
				}
			}

			// 1. Parse manifest
			cfgFile, err := spec.ParseConfigFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to parse manifest %s: %w", filePath, err)
			}

			// 2. Open state store & initialize ControlPlane instance
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster state store at %s: %w", dbPath, err)
			}
			defer store.Close()

			cp, err := controlplane.New(controlplane.Options{
				Config: cfg,
				Store:  store,
				Logger: logging.NewDefaultLogger(),
			})
			if err != nil {
				return fmt.Errorf("failed to initialize control plane: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Deploying services from %s...\n\n", filePath)

			for name, svcConfig := range cfgFile.Services {
				res, err := cp.DeployService(ctx, svcConfig, nil)
				if err != nil {
					fmt.Fprintf(out, " [FAILED] Service '%s': %v\n", name, err)
					continue
				}

				fmt.Fprintf(out, " [SUCCESS] Service '%s' (ID: %s)\n", res.ServiceName, res.ServiceID)
				fmt.Fprintf(out, "   Replicas: %d/%d assigned\n", len(res.Tasks), res.Replicas)
				fmt.Fprintf(out, "   Deployment: %s\n", res.DeploymentID)
				fmt.Fprintf(out, "   Status: %s\n\n", res.Status)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&manifestPath, "file", "f", "", "Path to service YAML manifest file")
	return cmd
}

func newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage and inspect CloudX services",
		Long:  `List running services, inspect deployment details, configurations, and replicas.`,
	}

	cmd.AddCommand(newServiceListCmd())
	cmd.AddCommand(newServiceInspectCmd())
	cmd.AddCommand(newServiceScaleCmd())
	return cmd
}

func newServiceListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all deployed services in the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster store: %w", err)
			}
			defer store.Close()

			services, err := store.Services().List(ctx)
			if err != nil {
				return fmt.Errorf("failed to list services: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(services)
			}

			if len(services) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No services deployed in the cluster.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "SERVICE ID\tNAME\tREPLICAS\tRUNTIME\tSTATUS\tUPDATED")
			for _, s := range services {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n",
					s.ID,
					s.Name,
					s.Replicas,
					s.Runtime,
					s.Status,
					s.UpdatedAt.Format(time.RFC3339),
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output service list as JSON")
	return cmd
}

func newServiceInspectCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "inspect <service-name-or-id>",
		Short: "Inspect detailed service information, deployments, and running replicas",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceNameOrID := args[0]

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster store: %w", err)
			}
			defer store.Close()

			cp, err := controlplane.New(controlplane.Options{
				Config: cfg,
				Store:  store,
				Logger: logging.NewDefaultLogger(),
			})
			if err != nil {
				return fmt.Errorf("failed to initialize control plane: %w", err)
			}

			inspectRes, err := cp.InspectService(ctx, serviceNameOrID)
			if err != nil {
				return fmt.Errorf("failed to inspect service: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(inspectRes)
			}

			svc := inspectRes.Service
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "SERVICE: %s\n", svc.Name)
			fmt.Fprintf(out, "  ID:          %s\n", svc.ID)
			fmt.Fprintf(out, "  Status:      %s\n", svc.Status)
			fmt.Fprintf(out, "  Replicas:    %d\n", svc.Replicas)
			fmt.Fprintf(out, "  Runtime:     %s\n", svc.Runtime)
			fmt.Fprintf(out, "  Command:     %s\n", svc.Command)
			fmt.Fprintf(out, "  Created:     %s\n", svc.CreatedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "  Updated:     %s\n\n", svc.UpdatedAt.Format(time.RFC3339))

			fmt.Fprintln(out, "DEPLOYMENTS:")
			if len(inspectRes.Deployments) == 0 {
				fmt.Fprintln(out, "  No deployment history.")
			} else {
				for _, d := range inspectRes.Deployments {
					fmt.Fprintf(out, "  - ID: %s | Version: %s | Status: %s | Created: %s\n",
						d.ID, d.Version, d.Status, d.CreatedAt.Format(time.RFC3339))
				}
			}
			fmt.Fprintln(out, "")

			fmt.Fprintln(out, "TASKS / REPLICAS:")
			if len(inspectRes.Tasks) == 0 {
				fmt.Fprintln(out, "  No running tasks.")
			} else {
				w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "  TASK ID\tWORKER\tSTATE\tPID\tUPDATED")
				for _, t := range inspectRes.Tasks {
					fmt.Fprintf(w, "  %s\t%s\t%s\t%d\t%s\n",
						t.ID,
						t.WorkerID,
						t.State,
						t.PID,
						t.UpdatedAt.Format(time.RFC3339),
					)
				}
				_ = w.Flush()
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output service inspection as JSON")
	return cmd
}

func newServiceScaleCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "scale <service-name-or-id> <replicas>",
		Short: "Scale the replica count of a service up or down",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceNameOrID := args[0]
			var replicas int
			if _, err := fmt.Sscanf(args[1], "%d", &replicas); err != nil || replicas < 0 {
				return fmt.Errorf("invalid replica count '%s': must be a non-negative integer", args[1])
			}

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster store: %w", err)
			}
			defer store.Close()

			cp, err := controlplane.New(controlplane.Options{
				Config: cfg,
				Store:  store,
				Logger: logging.NewDefaultLogger(),
			})
			if err != nil {
				return fmt.Errorf("failed to initialize control plane: %w", err)
			}

			res, err := cp.ScaleService(ctx, serviceNameOrID, replicas, nil)
			if err != nil {
				return fmt.Errorf("failed to scale service %s: %w", serviceNameOrID, err)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Service '%s' scaled successfully:\n", res.ServiceName)
			fmt.Fprintf(out, "  Previous Replicas: %d\n", res.PreviousReplicas)
			fmt.Fprintf(out, "  Desired Replicas:  %d\n", res.DesiredReplicas)
			fmt.Fprintf(out, "  Tasks Created:     %d\n", res.Summary.CreatedTasks)
			fmt.Fprintf(out, "  Tasks Removed:     %d\n", res.Summary.RemovedTasks)
			fmt.Fprintf(out, "  Status:            %s\n", res.Status)

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output scale result as JSON")
	return cmd
}

