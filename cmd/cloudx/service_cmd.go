package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "deploy [flags] [service:version | manifest-path]",
		Short: "Deploy services from a YAML manifest file or rollout a specific service version (e.g. api:v2)",
		Long: `Deploy one or more services into the CloudX cluster from a declarative YAML specification,
or deploy/switch to a specific immutable application version (e.g. cloudx deploy api:v2).

Examples:
  cloudx deploy service.yaml
  cloudx deploy -f service.yaml
  cloudx deploy api:v2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			// 1. Open state store & initialize ControlPlane instance
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

			targetArg := manifestPath
			if targetArg == "" && len(args) > 0 {
				targetArg = args[0]
			}

			// Check if targetArg is version syntax: service:version (e.g., "api:v2")
			// Make sure it's not a windows drive path like "D:\foo\bar.yaml" or "C:\test.yaml"
			isServiceVersion := false
			if targetArg != "" && strings.Contains(targetArg, ":") {
				parts := strings.SplitN(targetArg, ":", 2)
				// If first part is 1 character (e.g. "C", "D"), it's likely a Windows drive path if second part starts with \ or /
				if len(parts[0]) > 1 || (!strings.HasPrefix(parts[1], `\`) && !strings.HasPrefix(parts[1], "/")) {
					if !strings.HasSuffix(strings.ToLower(targetArg), ".yaml") && !strings.HasSuffix(strings.ToLower(targetArg), ".yml") && !strings.HasSuffix(strings.ToLower(targetArg), ".json") {
						isServiceVersion = true
					}
				}
			}

			if isServiceVersion {
				parts := strings.SplitN(targetArg, ":", 2)
				serviceName := parts[0]
				targetVersion := parts[1]

				fmt.Fprintf(out, "Deploying version '%s' for service '%s'...\n\n", targetVersion, serviceName)
				res, err := cp.DeployVersion(ctx, serviceName, targetVersion, nil)
				if err != nil {
					return fmt.Errorf("versioned deployment failed: %w", err)
				}

				if jsonOutput {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(res)
				}

				fmt.Fprintf(out, " [SUCCESS] Service '%s' (ID: %s)\n", res.ServiceName, res.ServiceID)
				fmt.Fprintf(out, "   Version:    %s\n", targetVersion)
				fmt.Fprintf(out, "   Deployment: %s\n", res.DeploymentID)
				fmt.Fprintf(out, "   Replicas:   %d/%d active\n", len(res.Tasks), res.Replicas)
				fmt.Fprintf(out, "   Status:     %s\n\n", res.Status)
				return nil
			}

			filePath := targetArg
			if filePath == "" {
				// Default fallback to cloudx.yaml or service.yaml in current dir
				if _, err := os.Stat("cloudx.yaml"); err == nil {
					filePath = "cloudx.yaml"
				} else if _, err := os.Stat("service.yaml"); err == nil {
					filePath = "service.yaml"
				} else {
					return fmt.Errorf("manifest file or service:version required: please specify via --file or argument")
				}
			}

			// Parse manifest
			cfgFile, err := spec.ParseConfigFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to parse manifest %s: %w", filePath, err)
			}

			fmt.Fprintf(out, "Deploying services from %s...\n\n", filePath)

			var results []*controlplane.DeployResult
			for name, svcConfig := range cfgFile.Services {
				res, err := cp.DeployService(ctx, svcConfig, nil)
				if err != nil {
					fmt.Fprintf(out, " [FAILED] Service '%s': %v\n", name, err)
					continue
				}
				results = append(results, res)

				fmt.Fprintf(out, " [SUCCESS] Service '%s' (ID: %s)\n", res.ServiceName, res.ServiceID)
				fmt.Fprintf(out, "   Replicas: %d/%d assigned\n", len(res.Tasks), res.Replicas)
				fmt.Fprintf(out, "   Deployment: %s\n", res.DeploymentID)
				fmt.Fprintf(out, "   Status: %s\n\n", res.Status)
			}

			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&manifestPath, "file", "f", "", "Path to service YAML manifest file")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output deployment result as JSON")
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

