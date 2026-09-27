package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/metrics"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

func newMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Observe and inspect CloudX cluster metrics",
		Long: `Collect and display CloudX observability metrics across control plane, workers, services, and jobs.

Phase 58 — Metrics Model (Milestone 16: Observability)

Metric domains:
  control-plane  reconciliation cycles, scheduling latency, RPC failures, state operations
  worker         CPU usage, memory, task count, process restarts
  service        replicas desired/running, health failures, restart count
  job            execution duration, success/failure/cancel counts`,
	}

	cmd.AddCommand(newMetricsShowCmd())
	return cmd
}

func newMetricsShowCmd() *cobra.Command {
	var jsonOutput bool
	var domain string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show current cluster metrics",
		Long: `Collect a point-in-time snapshot of all CloudX metrics from the state store and display them.

Available domains: all, controlplane, worker, service, job

Examples:
  cloudx metrics show
  cloudx metrics show --domain worker
  cloudx metrics show --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to open cluster database: %w", err)
			}
			defer store.Close()

			// Build cluster metrics and run store collection
			reg := metrics.NewRegistry()
			cm := metrics.NewClusterMetrics(reg)
			sc := metrics.NewStoreCollector(store, cm)

			result, err := sc.Collect(ctx)
			if err != nil {
				return fmt.Errorf("failed to collect metrics: %w", err)
			}

			snap := cm.Snapshot()

			// Apply domain filter
			domainFilter := strings.ToLower(strings.TrimSpace(domain))
			if domainFilter != "" && domainFilter != "all" {
				filtered := snap[:0]
				for _, mv := range snap {
					switch domainFilter {
					case "controlplane", "control-plane", "cp":
						if strings.HasPrefix(mv.Name, "cloudx_controlplane_") {
							filtered = append(filtered, mv)
						}
					case "worker":
						if strings.HasPrefix(mv.Name, "cloudx_worker_") {
							filtered = append(filtered, mv)
						}
					case "service":
						if strings.HasPrefix(mv.Name, "cloudx_service_") {
							filtered = append(filtered, mv)
						}
					case "job":
						if strings.HasPrefix(mv.Name, "cloudx_job_") {
							filtered = append(filtered, mv)
						}
					}
				}
				snap = filtered
			}

			if isJSONOutput(cmd, jsonOutput) {
				type jsonMetricsOutput struct {
					CollectedAt  string               `json:"collected_at"`
					Summary      *metrics.CollectResult `json:"summary"`
					Metrics      []metrics.MetricValue  `json:"metrics"`
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(jsonMetricsOutput{
					CollectedAt: time.Now().UTC().Format(time.RFC3339),
					Summary:     result,
					Metrics:     snap,
				})
			}

			// Human-readable output
			fmt.Fprintf(out, "CloudX Cluster Metrics — %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
			fmt.Fprintf(out, "Workers: %d  |  Services: %d  |  Jobs: %d (✓%d ✗%d)\n\n",
				result.Workers, result.Services, result.Jobs, result.JobsSucceeded, result.JobsFailed)

			tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)

			// Print by type
			var counters, gauges, histograms []metrics.MetricValue
			for _, mv := range snap {
				switch mv.Type {
				case "counter":
					counters = append(counters, mv)
				case "gauge":
					gauges = append(gauges, mv)
				case "histogram":
					histograms = append(histograms, mv)
				}
			}

			if len(counters) > 0 {
				fmt.Fprintln(tw, "── COUNTERS ──────────────────────────────────────────────────────────────────")
				fmt.Fprintln(tw, "METRIC\tVALUE")
				for _, mv := range counters {
					label := mv.Name
					if len(mv.Labels) > 0 {
						label = labelString(mv.Name, mv.Labels)
					}
					fmt.Fprintf(tw, "%s\t%.0f\n", label, mv.Value)
				}
				fmt.Fprintln(tw)
			}

			if len(gauges) > 0 {
				fmt.Fprintln(tw, "── GAUGES ───────────────────────────────────────────────────────────────────")
				fmt.Fprintln(tw, "METRIC\tVALUE")
				for _, mv := range gauges {
					label := mv.Name
					if len(mv.Labels) > 0 {
						label = labelString(mv.Name, mv.Labels)
					}
					fmt.Fprintf(tw, "%s\t%.4g\n", label, mv.Value)
				}
				fmt.Fprintln(tw)
			}

			if len(histograms) > 0 {
				fmt.Fprintln(tw, "── HISTOGRAMS ───────────────────────────────────────────────────────────────")
				fmt.Fprintln(tw, "METRIC\tCOUNT\tSUM(s)\tP50(s)\tP90(s)\tP99(s)")
				for _, mv := range histograms {
					label := mv.Name
					if len(mv.Labels) > 0 {
						label = labelString(mv.Name, mv.Labels)
					}
					fmt.Fprintf(tw, "%s\t%d\t%.4f\t%s\t%s\t%s\n",
						label, mv.Count, mv.Sum, fmtQ(mv.P50), fmtQ(mv.P90), fmtQ(mv.P99))
				}
				fmt.Fprintln(tw)
			}

			return tw.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output metrics as JSON")
	cmd.Flags().StringVar(&domain, "domain", "all", "Filter metrics by domain: all, controlplane, worker, service, job")
	return cmd
}

// labelString builds a short label annotation for display.
func labelString(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	var parts []string
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return fmt.Sprintf("%s{%s}", name, strings.Join(parts, ","))
}

// fmtQ formats a *float64 quantile value for display; returns "—" if nil (no observations).
func fmtQ(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%.4f", *p)
}
