package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/metrics"
	"github.com/cloudx-org/cloudx/internal/otel"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

func newOtelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "otel",
		Short: "Inspect OpenTelemetry-compatible tracing and telemetry status",
		Long: `Inspect OpenTelemetry tracing providers, recent in-memory trace spans, and export status.

Phase 59 — OpenTelemetry-Compatible Architecture (Milestone 16: Observability)

CloudX operates with zero required external infrastructure. Traces and metrics
are collected in-memory by default and can optionally be exported to any
OpenTelemetry Collector or compatible APM (Jaeger, Tempo, Datadog) via standard OTLP/HTTP.`,
	}

	cmd.AddCommand(newOtelStatusCmd())
	cmd.AddCommand(newOtelSpansCmd())
	cmd.AddCommand(newOtelMetricsCmd())
	return cmd
}

func newOtelStatusCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show OpenTelemetry telemetry provider status and configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			provider := otel.GetGlobalProvider()
			st := provider.Status()

			// Update provider status with active config if unconfigured
			if st.ServiceName == "cloudx" && cfg.Telemetry.ServiceName != "" {
				st.ServiceName = cfg.Telemetry.ServiceName
			}
			if st.OTLPEndpoint == "" && cfg.Telemetry.OTLPEndpoint != "" {
				st.OTLPEndpoint = cfg.Telemetry.OTLPEndpoint
				st.OTLPConfigured = true
			}

			if isJSONOutput(cmd, jsonOutput) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(st)
			}

			fmt.Fprintf(out, "OpenTelemetry Architecture Status — CloudX\n")
			fmt.Fprintf(out, "─────────────────────────────────────────────────────────────\n")
			fmt.Fprintf(out, "Service Name:        %s\n", st.ServiceName)
			fmt.Fprintf(out, "Service Version:     %s\n", st.ServiceVersion)
			fmt.Fprintf(out, "Telemetry Mode:      %s\n", func() string {
				if cfg.Telemetry.Disabled {
					return "DISABLED (no-op)"
				}
				if st.OTLPConfigured {
					return "HYBRID (In-Memory Ring Buffer + OTLP HTTP Export)"
				}
				return "STANDALONE (In-Memory Ring Buffer — Zero External Server Required)"
			}())
			fmt.Fprintf(out, "OTLP Endpoint:       %s\n", func() string {
				if st.OTLPEndpoint != "" {
					return st.OTLPEndpoint
				}
				return "none (set CLOUDX_OTEL_ENDPOINT to enable external export)"
			}())
			fmt.Fprintf(out, "Buffer Capacity:     %d spans\n", st.BufferCapacity)
			fmt.Fprintf(out, "Retained Spans:      %d\n", st.StoredSpans)
			fmt.Fprintf(out, "Total Collected:     %d\n", st.TotalCollected)
			fmt.Fprintf(out, "Active Exporters:    %d\n", st.ExporterCount)
			fmt.Fprintf(out, "\nResource Attributes:\n")
			for k, v := range st.ResourceSummary {
				fmt.Fprintf(out, "  %s: %s\n", k, v)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output status as JSON")
	return cmd
}

func newOtelSpansCmd() *cobra.Command {
	var jsonOutput bool
	var limit int

	cmd := &cobra.Command{
		Use:   "spans",
		Short: "List recent in-memory trace spans recorded by CloudX",
		Long:  "Inspect the in-memory ring buffer of OpenTelemetry trace spans recorded during operations.",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			provider := otel.GetGlobalProvider()
			spans := provider.RingBuffer().Spans()

			if limit > 0 && len(spans) > limit {
				spans = spans[len(spans)-limit:]
			}

			if isJSONOutput(cmd, jsonOutput) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(spans)
			}

			if len(spans) == 0 {
				fmt.Fprintln(out, "No trace spans recorded yet in buffer.")
				return nil
			}

			fmt.Fprintf(out, "Recent OpenTelemetry Spans (%d retained):\n\n", len(spans))
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TIMESTAMP\tKIND\tNAME\tTRACE_ID\tSPAN_ID\tDUR(ms)\tSTATUS")
			for _, s := range spans {
				traceShort := s.SpanContext.TraceID.String()
				if len(traceShort) > 8 {
					traceShort = traceShort[:8] + ".."
				}
				spanShort := s.SpanContext.SpanID.String()
				if len(spanShort) > 8 {
					spanShort = spanShort[:8] + ".."
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%.2f\t%s\n",
					s.StartTime.UTC().Format("15:04:05.000"),
					s.SpanKind,
					s.Name,
					traceShort,
					spanShort,
					s.DurationMs,
					s.Status.Code,
				)
			}
			return tw.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output spans as JSON")
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "Maximum number of recent spans to display")
	return cmd
}

func newOtelMetricsCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Inspect OpenTelemetry ResourceMetrics bridged from CloudX state",
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

			reg := metrics.NewRegistry()
			cm := metrics.NewClusterMetrics(reg)
			sc := metrics.NewStoreCollector(store, cm)
			_, _ = sc.Collect(ctx)

			bridge := otel.NewMetricsBridge(reg, map[string]string{
				"service.name":    "cloudx",
				"service.version": "0.1.0",
				"host.name":       cfg.Node.Name,
			}, "cloudx.observability", "0.1.0")

			rm, err := bridge.Collect(ctx)
			if err != nil {
				return fmt.Errorf("failed to bridge metrics to OpenTelemetry format: %w", err)
			}

			if isJSONOutput(cmd, jsonOutput) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rm)
			}

			fmt.Fprintf(out, "OpenTelemetry ResourceMetrics Bridge — Scope: cloudx.observability\n\n")
			tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "METRIC\tTYPE\tUNIT\tDESCRIPTION\tVALUE/COUNT")

			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					valStr := "—"
					if len(m.DataPoints) > 0 {
						valStr = fmt.Sprintf("%.4g", m.DataPoints[0].Value)
					} else if len(m.HistPoints) > 0 {
						valStr = fmt.Sprintf("count=%d sum=%.4f", m.HistPoints[0].Count, m.HistPoints[0].Sum)
					}
					unit := m.Unit
					if unit == "" {
						unit = "1"
					}
					desc := m.Description
					if len(desc) > 40 {
						desc = desc[:37] + "..."
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
						m.Name, m.Type, unit, desc, valStr)
				}
			}
			return tw.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output OpenTelemetry metrics as JSON")
	return cmd
}
