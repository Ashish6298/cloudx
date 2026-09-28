package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/auth"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/logs"
	"github.com/cloudx-org/cloudx/internal/scheduler"
	"github.com/cloudx-org/cloudx/internal/spec"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	v1 "github.com/cloudx-org/cloudx/proto/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func newJobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "job",
		Aliases: []string{"jobs"},
		Short:   "Manage and execute finite batch workloads (Jobs)",
		Long: `Run, inspect, monitor, and manage finite batch jobs in the CloudX cluster.
Jobs utilize the same underlying scheduler, worker daemon, native runtime, events, and log systems as long-running services.`,
	}

	cmd.AddCommand(newJobRunCmd())
	cmd.AddCommand(newJobListCmd())
	cmd.AddCommand(newJobInspectCmd())
	cmd.AddCommand(newJobLogsCmd())
	cmd.AddCommand(newJobCancelCmd())
	cmd.AddCommand(newJobRetryCmd())
	return cmd
}

func newJobRunCmd() *cobra.Command {
	var manifestPath string
	var commandStr string
	var argsList []string
	var envMap map[string]string
	var timeoutStr string
	var maxRetries int
	var backoffStr string
	var runtimeType string
	var cpuStr string
	var memStr string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "run [job-name | manifest-path]",
		Short: "Execute a finite batch job (e.g. cloudx job run migration)",
		Long: `Run a finite batch workload on the cluster using CloudX's unified scheduling and worker execution engine.

Examples:
  cloudx job run migration --command "goose up"
  cloudx job run migration --command "python run.py" --timeout 15m --max-retries 3
  cloudx job run -f job.yaml
  cloudx job run migration.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			// 1. Resolve Job Configuration
			var targetJobConfig *spec.JobConfig

			var targetArg string
			if len(args) > 0 {
				targetArg = args[0]
			}

			if manifestPath == "" && targetArg != "" {
				if strings.HasSuffix(targetArg, ".yaml") || strings.HasSuffix(targetArg, ".yml") {
					manifestPath = targetArg
				}
			}

			if manifestPath != "" {
				cfgFile, err := spec.ParseJobConfigFile(manifestPath)
				if err != nil {
					return fmt.Errorf("failed to parse job manifest %s: %w", manifestPath, err)
				}
				if len(cfgFile.Jobs) == 0 {
					return fmt.Errorf("manifest %s contains no job specifications", manifestPath)
				}
				// Pick specified job name or the first job
				if targetArg != "" && targetArg != manifestPath {
					if j, ok := cfgFile.Jobs[targetArg]; ok {
						targetJobConfig = j
					} else {
						return fmt.Errorf("job '%s' not found in manifest %s", targetArg, manifestPath)
					}
				} else {
					for _, j := range cfgFile.Jobs {
						targetJobConfig = j
						break
					}
				}
			} else {
				// Ad-hoc job command execution: cloudx job run <name> --command "<cmd>"
				jobName := targetArg
				if jobName == "" {
					jobName = fmt.Sprintf("job-%d", time.Now().Unix())
				}
				if commandStr == "" {
					return fmt.Errorf("either a manifest file (-f) or --command is required to run a job")
				}

				targetJobConfig = &spec.JobConfig{
					Name:        jobName,
					Command:     commandStr,
					Args:        argsList,
					Environment: envMap,
					Runtime:     runtimeType,
					Timeout:     timeoutStr,
					Resources: spec.ResourceConfig{
						CPU:    cpuStr,
						Memory: memStr,
					},
					RetryPolicy: &spec.JobRetrySpec{
						MaxRetries:    maxRetries,
						BackoffPeriod: backoffStr,
					},
				}
			}

			// 2. Load Configuration & State Store
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

			// 3. Setup RPC Dispatcher to Workers
			dispatcher := scheduler.NewInProcessDispatcher()

			// Query active workers to register network dispatchers
			workers, err := store.Workers().List(ctx)
			if err == nil {
				for _, w := range workers {
					workerCopy := w
					if workerCopy.Address != "" {
						dispatcher.RegisterWorkerHandler(workerCopy.ID, func(dCtx context.Context, req *v1.TaskAssignmentRequest) error {
							conn, err := grpc.DialContext(dCtx, workerCopy.Address,
								grpc.WithTransportCredentials(insecure.NewCredentials()),
								grpc.WithBlock(),
							)
							if err != nil {
								return fmt.Errorf("failed to dial worker %s (%s): %w", workerCopy.ID, workerCopy.Address, err)
							}
							defer conn.Close()

							client := v1.NewControlPlaneServiceClient(conn)
							resp, err := client.AssignTask(dCtx, req)
							if err != nil {
								return fmt.Errorf("worker %s assignment RPC failed: %w", workerCopy.ID, err)
							}
							if !resp.Accepted {
								return fmt.Errorf("worker %s rejected job assignment: %s", workerCopy.ID, resp.Message)
							}
							return nil
						})
					} else {
						// Local in-process execution fallback
						dispatcher.RegisterWorkerHandler(workerCopy.ID, func(dCtx context.Context, req *v1.TaskAssignmentRequest) error {
							return nil
						})
					}
				}
			}

			// 4. Run Job Execution
			result, err := cp.RunJob(ctx, targetJobConfig, dispatcher)
			if err != nil {
				return fmt.Errorf("job execution failed: %w", err)
			}

			if isJSONOutput(cmd, jsonOutput) {
				return writeJSON(out, result)
			}

			fmt.Fprintf(out, "Job '%s' submitted successfully!\n\n", result.JobName)
			w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "JOB ID\tNAME\tSTATE\tWORKER\tTASK ID\tCONFIG HASH")
			shortHash := result.ConfigHash
			if len(shortHash) > 12 {
				shortHash = shortHash[:12]
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				result.JobID,
				result.JobName,
				result.State,
				result.WorkerID,
				result.TaskID,
				shortHash,
			)
			w.Flush()
			fmt.Fprintf(out, "\nTo inspect logs: cloudx job logs %s\n", result.JobName)
			return nil
		},
	}

	cmd.Flags().StringVarP(&manifestPath, "file", "f", "", "Path to YAML job manifest file")
	cmd.Flags().StringVar(&commandStr, "command", "", "Command to execute for ad-hoc job")
	cmd.Flags().StringSliceVar(&argsList, "args", nil, "Arguments for job command")
	cmd.Flags().StringToStringVar(&envMap, "env", nil, "Environment variables (KEY=VALUE)")
	cmd.Flags().StringVar(&timeoutStr, "timeout", "", "Job execution timeout (e.g. 10m, 1h)")
	cmd.Flags().IntVar(&maxRetries, "max-retries", 0, "Maximum retry attempts upon failure")
	cmd.Flags().StringVar(&backoffStr, "backoff", "5s", "Retry backoff period (e.g. 5s, 10s)")
	cmd.Flags().StringVar(&runtimeType, "runtime", "native", "Runtime type (native, docker)")
	cmd.Flags().StringVar(&cpuStr, "cpu", "500m", "CPU resource allocation (e.g. 500m, 1.0)")
	cmd.Flags().StringVar(&memStr, "memory", "256MiB", "Memory resource allocation (e.g. 256MiB, 1GB)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result in JSON format")

	return cmd
}

func newJobListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all finite jobs in the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster state store: %w", err)
			}
			defer store.Close()

			jobs, err := store.Jobs().List(ctx)
			if err != nil {
				return fmt.Errorf("failed to list jobs: %w", err)
			}

			if isJSONOutput(cmd, jsonOutput) {
				var records []*models.JobRecord
				for _, j := range jobs {
					rec, _ := models.JobFromModel(j)
					if rec != nil {
						records = append(records, rec)
					}
				}
				return writeJSON(out, records)
			}

			if len(jobs) == 0 {
				fmt.Fprintln(out, "No jobs found in cluster.")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
			fmt.Fprintln(w, "JOB ID\tNAME\tSTATE\tCOMMAND\tASSIGNED WORKER\tCREATED")

			for _, j := range jobs {
				rec, _ := models.JobFromModel(j)
				assignedWorker := "-"
				if rec != nil && rec.AssignedTo != "" {
					assignedWorker = rec.AssignedTo.String()
				}
				cmdDisplay := j.Command
				if len(cmdDisplay) > 30 {
					cmdDisplay = cmdDisplay[:27] + "..."
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					j.ID,
					j.Name,
					j.Status,
					cmdDisplay,
					assignedWorker,
					j.CreatedAt.Format("2006-01-02 15:04:05"),
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

func newJobInspectCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "inspect [job-name | job-id]",
		Short: "Display detailed execution status and configuration of a job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			jobNameOrID := args[0]

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster state store: %w", err)
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

			result, err := cp.InspectJob(ctx, jobNameOrID)
			if err != nil {
				return err
			}

			if isJSONOutput(cmd, jsonOutput) {
				return writeJSON(out, result)
			}

			j := result.Job
			fmt.Fprintf(out, "Job: %s\n", j.Name)
			fmt.Fprintf(out, "  ID:            %s\n", j.ID)
			fmt.Fprintf(out, "  State:         %s\n", j.State)
			fmt.Fprintf(out, "  Command:       %s\n", auth.RedactString(j.Config.Command))
			if len(j.Config.Args) > 0 {
				redactedArgs := auth.RedactCommandArgs(j.Config.Args)
				fmt.Fprintf(out, "  Args:          %s\n", strings.Join(redactedArgs, " "))
			}
			if len(j.Config.Environment) > 0 {
				fmt.Fprintln(out, "  Environment:")
				redactedEnv := auth.RedactEnvironmentVariables(j.Config.Environment)
				for k, v := range redactedEnv {
					fmt.Fprintf(out, "    - %s: %s\n", k, v)
				}
			}
			fmt.Fprintf(out, "  Runtime:       %s\n", j.Config.Runtime)
			fmt.Fprintf(out, "  Config Hash:   %s\n", j.ConfigHash)
			if j.AssignedTo != "" {
				fmt.Fprintf(out, "  Worker:        %s\n", j.AssignedTo)
			}
			if j.TaskID != "" {
				fmt.Fprintf(out, "  Task ID:       %s\n", j.TaskID)
			}
			if j.ExitCode != 0 {
				fmt.Fprintf(out, "  Exit Code:     %d\n", j.ExitCode)
			}
			if j.Config.Timeout > 0 {
				fmt.Fprintf(out, "  Timeout:       %v\n", j.Config.Timeout)
			}
			if j.Config.RetryPolicy.MaxRetries > 0 {
				fmt.Fprintf(out, "  Max Retries:   %d (Backoff: %v)\n", j.Config.RetryPolicy.MaxRetries, j.Config.RetryPolicy.BackoffPeriod)
			}
			fmt.Fprintf(out, "  Created:       %s\n", j.CreatedAt.Format("2006-01-02 15:04:05"))

			if result.Task != nil {
				fmt.Fprintln(out, "\nTask Details:")
				fmt.Fprintf(out, "  Task State:    %s\n", result.Task.State)
				fmt.Fprintf(out, "  PID:           %d\n", result.Task.PID)
				fmt.Fprintf(out, "  Exit Code:     %d\n", result.Task.ExitCode)
				fmt.Fprintf(out, "  Updated:       %s\n", result.Task.UpdatedAt.Format("2006-01-02 15:04:05"))
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

func newJobLogsCmd() *cobra.Command {
	var follow bool
	var tail int
	var sinceStr string

	cmd := &cobra.Command{
		Use:   "logs [job-name | job-id]",
		Short: "Fetch and stream stdout/stderr logs from a job workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			jobNameOrID := args[0]

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster state store: %w", err)
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

			filter := logs.LogFilter{
				TailLines: tail,
				Follow:    follow,
			}
			if sinceStr != "" {
				if d, err := time.ParseDuration(sinceStr); err == nil {
					filter.Since = time.Now().UTC().Add(-d)
				}
			}

			entries, taskIDs, err := cp.GetJobLogs(ctx, jobNameOrID, filter)
			if err != nil {
				return err
			}

			// Print historical buffered/disk logs
			for _, entry := range entries {
				fmt.Fprintf(out, "[%s] [%s] %s\n",
					entry.Timestamp.Format("2006-01-02 15:04:05"),
					entry.Stream,
					entry.Message,
				)
			}

			if !follow {
				return nil
			}

			// Live Follow Streaming
			logger := logs.DefaultWorkloadLogger()
			if cfg.Storage.Path != "" {
				logger.SetBaseDir(filepath.Join(cfg.Storage.Path, "logs"))
			}

			logStream := logger.SubscribeFilter(ctx, filter, taskIDs)
			for entry := range logStream {
				fmt.Fprintf(out, "[%s] [%s] %s\n",
					entry.Timestamp.Format("2006-01-02 15:04:05"),
					entry.Stream,
					entry.Message,
				)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output in real time")
	cmd.Flags().IntVarP(&tail, "tail", "n", 0, "Number of recent lines to display")
	cmd.Flags().StringVar(&sinceStr, "since", "", "Show logs since relative duration (e.g. 5m, 1h)")

	return cmd
}

func newJobCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel [job-name | job-id]",
		Short: "Cancel an active or running job workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			jobNameOrID := args[0]

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster state store: %w", err)
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

			if err := cp.CancelJob(ctx, jobNameOrID); err != nil {
				return fmt.Errorf("failed to cancel job: %w", err)
			}

			fmt.Fprintf(out, "Job '%s' cancelled successfully.\n", jobNameOrID)
			return nil
		},
	}
	return cmd
}

func newJobRetryCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "retry [job-name | job-id]",
		Short: "Retry a failed job workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			jobNameOrID := args[0]

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster state store: %w", err)
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

			// Setup RPC Dispatcher to Workers
			dispatcher := scheduler.NewInProcessDispatcher()
			workers, err := store.Workers().List(ctx)
			if err == nil {
				for _, w := range workers {
					workerCopy := w
					if workerCopy.Address != "" {
						dispatcher.RegisterWorkerHandler(workerCopy.ID, func(dCtx context.Context, req *v1.TaskAssignmentRequest) error {
							conn, err := grpc.DialContext(dCtx, workerCopy.Address,
								grpc.WithTransportCredentials(insecure.NewCredentials()),
								grpc.WithBlock(),
							)
							if err != nil {
								return fmt.Errorf("failed to dial worker %s: %w", workerCopy.ID, err)
							}
							defer conn.Close()

							client := v1.NewControlPlaneServiceClient(conn)
							resp, err := client.AssignTask(dCtx, req)
							if err != nil {
								return err
							}
							if !resp.Accepted {
								return fmt.Errorf("worker rejected retry: %s", resp.Message)
							}
							return nil
						})
					} else {
						dispatcher.RegisterWorkerHandler(workerCopy.ID, func(dCtx context.Context, req *v1.TaskAssignmentRequest) error {
							return nil
						})
					}
				}
			}

			result, err := cp.RetryJob(ctx, jobNameOrID, dispatcher)
			if err != nil {
				return fmt.Errorf("failed to retry job: %w", err)
			}

			if isJSONOutput(cmd, jsonOutput) {
				return writeJSON(out, result)
			}

			fmt.Fprintf(out, "Job '%s' retry scheduled successfully (Task ID: %s, Worker: %s)\n",
				result.JobName, result.TaskID, result.WorkerID)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}
