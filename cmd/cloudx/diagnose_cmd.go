package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/diagnostics"
	"github.com/cloudx-org/cloudx/internal/state"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

func newDiagnoseCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:     "diagnose",
		Aliases: []string{"doctor", "diag"},
		Short:   "Run comprehensive cluster and node health diagnostics",
		Long: `Run comprehensive diagnostics across 9 core CloudX vectors to instantly identify problems:

1. Control plane health (reachability, listener status)
2. Worker connectivity (registered workers, TCP status)
3. Database integrity (SQLite schema, PRAGMA integrity_check, foreign keys)
4. Heartbeat status (missed heartbeats, suspected/unhealthy/lost workers)
5. Scheduler status (schedulable workers in READY state)
6. Orphaned tasks (stranded tasks on lost or missing workers)
7. Failed deployments (degraded services, halted rollouts)
8. Resource pressure (host CPU/memory load and worker exhaustion)
9. Configuration problems (semantic validation, storage permissions)

Phase 60 — Diagnostics (Milestone 16: Observability)

Examples:
  cloudx diagnose
  cloudx diagnose --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cfg, err := config.Load(cliOpts)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			var store state.Store
			sqliteStore, err := sqlite.Open(ctx, dbPath)
			if err == nil {
				store = sqliteStore
				defer sqliteStore.Close()
			}

			engine := diagnostics.NewEngine(cfg, store, dbPath)
			report := engine.Run(ctx)

			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}

			// Human-readable formatting
			fmt.Fprintf(out, "CloudX Cluster Diagnostics — %s (Node: %s)\n",
				report.Timestamp.Format("2006-01-02 15:04:05 UTC"), report.NodeName)
			fmt.Fprintf(out, "─────────────────────────────────────────────────────────────────────────────\n")

			overallBadge := " [PASS] "
			switch report.Overall {
			case diagnostics.StatusWarn:
				overallBadge = " [WARN] "
			case diagnostics.StatusFail:
				overallBadge = " [FAIL] "
			}

			fmt.Fprintf(out, "Overall Status: %s (Passed: %d, Warnings: %d, Failures: %d | %.2fms)\n\n",
				overallBadge, report.PassedCount, report.WarnCount, report.FailCount, report.DurationMs)

			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "STATUS\tCATEGORY\tCHECK\tSUMMARY")
			for _, c := range report.Checks {
				badge := "✓ PASS"
				switch c.Status {
				case diagnostics.StatusWarn:
					badge = "▲ WARN"
				case diagnostics.StatusFail:
					badge = "✗ FAIL"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", badge, c.Category, c.Name, c.Summary)
			}
			_ = tw.Flush()

			// Print remediations if warnings or failures exist
			hasIssues := false
			for _, c := range report.Checks {
				if c.Status != diagnostics.StatusPass && c.Remediation != "" {
					if !hasIssues {
						fmt.Fprintf(out, "\nSuggested Remediations:\n")
						hasIssues = true
					}
					fmt.Fprintf(out, "  • [%s] %s: %s\n", c.Status, c.Name, c.Remediation)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output diagnostic report as JSON")
	return cmd
}
