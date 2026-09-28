package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/logs"
	"github.com/cloudx-org/cloudx/internal/registry"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
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
			ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
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

			isJSON := isJSONOutput(cmd, jsonOutput)

			if isServiceVersion {
				parts := strings.SplitN(targetArg, ":", 2)
				serviceName := parts[0]
				targetVersion := parts[1]

				if !isJSON {
					fmt.Fprintf(out, "Deploying version '%s' for service '%s'...\n\n", targetVersion, serviceName)
				}
				res, err := cp.DeployVersion(ctx, serviceName, targetVersion, nil)
				if err != nil {
					return fmt.Errorf("versioned deployment failed: %w", err)
				}

				if isJSON {
					return writeJSON(out, res)
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

			if !isJSON {
				fmt.Fprintf(out, "Deploying services from %s...\n\n", filePath)
			}

			var results []*controlplane.DeployResult
			for name, svcConfig := range cfgFile.Services {
				res, err := cp.DeployService(ctx, svcConfig, nil)
				if err != nil {
					if !isJSON {
						fmt.Fprintf(out, " [FAILED] Service '%s': %v\n", name, err)
					}
					continue
				}
				results = append(results, res)

				if !isJSON {
					fmt.Fprintf(out, " [SUCCESS] Service '%s' (ID: %s)\n", res.ServiceName, res.ServiceID)
					fmt.Fprintf(out, "   Replicas: %d/%d assigned\n", len(res.Tasks), res.Replicas)
					fmt.Fprintf(out, "   Deployment: %s\n", res.DeploymentID)
					fmt.Fprintf(out, "   Status: %s\n\n", res.Status)
				}
			}

			if isJSON {
				return writeJSON(out, results)
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
		Long:  `List running services, inspect deployment details, configurations, replicas, endpoints, and stream logs.`,
	}

	cmd.AddCommand(newServiceListCmd())
	cmd.AddCommand(newServiceInspectCmd())
	cmd.AddCommand(newServiceScaleCmd())
	cmd.AddCommand(newServiceRestartCmd())
	cmd.AddCommand(newServiceEndpointsCmd())
	cmd.AddCommand(newServiceLogsCmd())
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
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

			if isJSONOutput(cmd, jsonOutput) {
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
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

			if isJSONOutput(cmd, jsonOutput) {
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
			fmt.Fprintf(out, "  Command:     %s\n", auth.RedactString(svc.Command))

			// Parse embedded spec_json to display args and env
			if svc.SpecJSON != "" {
				var specCfg spec.ServiceConfig
				if err := json.Unmarshal([]byte(svc.SpecJSON), &specCfg); err == nil {
					if len(specCfg.Args) > 0 {
						redactedArgs := auth.RedactCommandArgs(specCfg.Args)
						fmt.Fprintf(out, "  Args:        %s\n", strings.Join(redactedArgs, " "))
					}
					if len(specCfg.Environment) > 0 {
						fmt.Fprintln(out, "  Environment:")
						redactedEnv := auth.RedactEnvironmentVariables(specCfg.Environment)
						for k, v := range redactedEnv {
							fmt.Fprintf(out, "    - %s: %s\n", k, v)
						}
					}
				}
			}

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
			ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
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

			if isJSONOutput(cmd, jsonOutput) {
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

func newServiceRestartCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "restart <service-name-or-id>",
		Short: "Restart all tasks for a service and trigger rolling recovery",
		Long: `Stop existing running tasks for a service to force a fresh restart and reconciliation across cluster workers.

Examples:
  cloudx service restart api
  cloudx service restart srv-18f45a2b-8a7f9b2c
  cloudx service restart api --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceNameOrID := args[0]
			out := cmd.OutOrStdout()

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
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
				return err
			}

			svc := inspectRes.Service
			now := time.Now().UTC()

			// Mark all currently running/active tasks for this service as stopped
			stoppedCount := 0
			for _, t := range inspectRes.Tasks {
				if t.State != string(models.TaskStateStopped) && t.State != string(models.TaskStateFailed) && t.State != string(models.TaskStateLost) {
					t.State = string(models.TaskStateStopped)
					t.UpdatedAt = now
					_ = store.Tasks().Update(ctx, t)
					stoppedCount++
				}
			}

			// Append SERVICE_RESTARTED audit event
			_ = store.Events().Append(ctx, &models.Event{
				ID:        id.NewEventID(),
				Type:      "SERVICE_RESTARTED",
				Source:    "controlplane",
				EntityID:  svc.ID,
				Payload:   fmt.Sprintf(`{"service":"%s","stopped_tasks":%d}`, svc.Name, stoppedCount),
				CreatedAt: now,
			})

			// Run reconciliation to reschedule new replacement tasks
			summary, err := cp.Reconciler.ReconcileAll(ctx)
			if err != nil {
				return fmt.Errorf("reconciliation after service restart failed: %w", err)
			}

			type restartResult struct {
				ServiceID    id.ID                              `json:"service_id"`
				ServiceName  string                             `json:"service_name"`
				StoppedTasks int                                `json:"stopped_tasks"`
				Summary      controlplane.ReconciliationSummary `json:"summary"`
				RestartedAt  time.Time                          `json:"restarted_at"`
			}

			res := restartResult{
				ServiceID:    svc.ID,
				ServiceName:  svc.Name,
				StoppedTasks: stoppedCount,
				Summary:      *summary,
				RestartedAt:  now,
			}

			if isJSONOutput(cmd, jsonOutput) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, "Service '%s' restarted successfully:\n", svc.Name)
			fmt.Fprintf(out, "  Stopped Tasks:     %d\n", stoppedCount)
			fmt.Fprintf(out, "  New Tasks Created: %d\n", summary.CreatedTasks)
			fmt.Fprintf(out, "  Status:            RESTARTED\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output restart result as JSON")
	return cmd
}

func newServiceLogsCmd() *cobra.Command {
	var (
		follow          bool
		tailLines       int
		taskIDStr       string
		deploymentIDStr string
		workerIDStr     string
		sinceStr        string
		jsonOutput      bool
	)

	cmd := &cobra.Command{
		Use:   "logs <service-name-or-id>",
		Short: "Fetch and stream stdout/stderr logs from CloudX workloads",
		Long: `Inspect and tail stdout/stderr output from processes managed by CloudX.
Logs are correlated with service, deployment, task, and worker metadata.

Examples:
  cloudx service logs api
  cloudx service logs api --follow
  cloudx service logs api --tail 50
  cloudx service logs api --task task-abc123
  cloudx service logs api --deployment dep-xyz789`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			serviceNameOrID := args[0]
			out := cmd.OutOrStdout()

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithCancel(cmd.Context())
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

			var sinceTime time.Time
			if sinceStr != "" {
				if d, err := time.ParseDuration(sinceStr); err == nil {
					sinceTime = time.Now().UTC().Add(-d)
				} else if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
					sinceTime = t
				} else {
					return fmt.Errorf("invalid --since format '%s': expected duration (e.g. 1h, 30m) or RFC3339 timestamp", sinceStr)
				}
			}

			filter := logs.LogFilter{
				DeploymentID: id.ID(deploymentIDStr),
				TaskID:       id.ID(taskIDStr),
				WorkerID:     id.ID(workerIDStr),
				Since:        sinceTime,
				TailLines:    tailLines,
				Follow:       follow,
			}

			entries, taskIDs, err := cp.GetServiceLogs(ctx, serviceNameOrID, filter)
			if err != nil {
				return fmt.Errorf("failed to fetch service logs: %w", err)
			}

			// Format and display initial historical entries
			isJSON := isJSONOutput(cmd, jsonOutput)
			for _, entry := range entries {
				printLogEntry(out, entry, isJSON)
			}

			// If follow requested, subscribe to live logs
			if follow {
				logger := logs.DefaultWorkloadLogger()
				if cfg.Storage.Path != "" {
					logger.SetBaseDir(filepath.Join(cfg.Storage.Path, "logs"))
				}

				liveCh := logger.SubscribeFilter(ctx, filter, taskIDs)
				for {
					select {
					case <-ctx.Done():
						return nil
					case entry, ok := <-liveCh:
						if !ok {
							return nil
						}
						printLogEntry(out, entry, isJSON)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log stream in real time")
	cmd.Flags().IntVarP(&tailLines, "tail", "n", 0, "Number of lines to show from the end of the logs")
	cmd.Flags().StringVar(&taskIDStr, "task", "", "Filter logs by specific task ID")
	cmd.Flags().StringVar(&deploymentIDStr, "deployment", "", "Filter logs by specific deployment ID")
	cmd.Flags().StringVar(&workerIDStr, "worker", "", "Filter logs by specific worker ID")
	cmd.Flags().StringVar(&sinceStr, "since", "", "Filter logs after duration (e.g. 1h, 15m) or RFC3339 timestamp")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output log entries as JSON lines")

	return cmd
}

func newServiceEndpointsCmd() *cobra.Command {
	var jsonOutput bool
	var networkFilter string

	cmd := &cobra.Command{
		Use:   "endpoints [service-name-or-id]",
		Short: "List active healthy endpoints for services",
		Long: `Query the service discovery registry for reachable, healthy task endpoints.
If a service name or ID is provided, displays endpoints for that service.
If omitted, lists endpoints across all services in the cluster.
Filter by logical network using the --network flag.

Examples:
  cloudx service endpoints api
  cloudx service endpoints
  cloudx service endpoints --network backend
  cloudx service endpoints api --network backend
  cloudx service endpoints --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

			if len(args) > 0 {
				target := args[0]
				var endpoints []*registry.Endpoint
				if networkFilter != "" {
					endpoints, err = cp.ResolveServiceInNetwork(ctx, target, networkFilter)
				} else {
					endpoints, err = cp.GetServiceEndpoints(ctx, target)
				}
				if err != nil {
					return fmt.Errorf("failed to get endpoints for %s: %w", target, err)
				}

				if isJSONOutput(cmd, jsonOutput) {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(endpoints)
				}

				if len(endpoints) == 0 {
					if networkFilter != "" {
						fmt.Fprintf(out, "No active healthy endpoints found for service '%s' in network '%s'.\n", target, networkFilter)
					} else {
						fmt.Fprintf(out, "No active healthy endpoints found for service '%s'.\n", target)
					}
					return nil
				}

				w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "SERVICE\tTASK ID\tWORKER ID\tENDPOINT\tPROTOCOL\tHEALTHY")
				for _, ep := range endpoints {
					healthyStr := "true"
					if !ep.Healthy {
						healthyStr = "false"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
						ep.ServiceName,
						ep.TaskID,
						ep.WorkerID,
						ep.Address,
						ep.Protocol,
						healthyStr,
					)
				}
				return w.Flush()
			}

			// If network filter is provided with no service argument, list all endpoints in network
			if networkFilter != "" {
				endpoints, err := cp.ResolveNetwork(ctx, networkFilter)
				if err != nil {
					return fmt.Errorf("failed to list endpoints in network %s: %w", networkFilter, err)
				}

				if isJSONOutput(cmd, jsonOutput) {
					enc := json.NewEncoder(out)
					enc.SetIndent("", "  ")
					return enc.Encode(endpoints)
				}

				if len(endpoints) == 0 {
					fmt.Fprintf(out, "No active service endpoints found in network '%s'.\n", networkFilter)
					return nil
				}

				w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "SERVICE\tTASK ID\tWORKER ID\tENDPOINT\tPROTOCOL\tHEALTHY")
				for _, ep := range endpoints {
					healthyStr := "true"
					if !ep.Healthy {
						healthyStr = "false"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
						ep.ServiceName,
						ep.TaskID,
						ep.WorkerID,
						ep.Address,
						ep.Protocol,
						healthyStr,
					)
				}
				return w.Flush()
			}

			// List all endpoints
			allEndpoints, err := cp.ListAllEndpoints(ctx)
			if err != nil {
				return fmt.Errorf("failed to list all endpoints: %w", err)
			}

			if isJSONOutput(cmd, jsonOutput) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(allEndpoints)
			}

			totalCount := 0
			for _, eps := range allEndpoints {
				totalCount += len(eps)
			}

			if totalCount == 0 {
				fmt.Fprintln(out, "No active service endpoints found in the cluster registry.")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "SERVICE\tTASK ID\tWORKER ID\tENDPOINT\tPROTOCOL\tHEALTHY")
			for _, eps := range allEndpoints {
				for _, ep := range eps {
					healthyStr := "true"
					if !ep.Healthy {
						healthyStr = "false"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
						ep.ServiceName,
						ep.TaskID,
						ep.WorkerID,
						ep.Address,
						ep.Protocol,
						healthyStr,
					)
				}
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output endpoints as JSON")
	cmd.Flags().StringVarP(&networkFilter, "network", "n", "", "Filter endpoints by logical network name")
	return cmd
}

func printLogEntry(w io.Writer, entry logs.LogEntry, asJSON bool) {
	if asJSON {
		b, _ := json.Marshal(entry)
		_, _ = fmt.Fprintln(w, string(b))
		return
	}

	ts := entry.Timestamp.Format(time.RFC3339)
	prefix := fmt.Sprintf("[%s]", ts)
	if entry.TaskID != "" {
		prefix = fmt.Sprintf("%s [%s]", prefix, entry.TaskID)
	}
	if entry.Stream != "" {
		prefix = fmt.Sprintf("%s [%s]", prefix, entry.Stream)
	}
	_, _ = fmt.Fprintf(w, "%s %s\n", prefix, entry.Message)
}
