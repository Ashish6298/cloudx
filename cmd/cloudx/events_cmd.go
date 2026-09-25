package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/id"
	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/events"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

func newEventsCmd() *cobra.Command {
	var serviceFilter string
	var nodeFilter string
	var typeFilter string
	var sinceStr string
	var limit int
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "events [flags]",
		Short: "Display persistent cluster events chronologically",
		Long: `Query and display cluster events in chronological order.
Filter events by service, node/worker, event type, or time window to inspect and debug workloads.

Examples:
  cloudx events
  cloudx events --service api
  cloudx events --node worker-1
  cloudx events --since 1h
  cloudx events --type WORKER_LOST --since 24h
  cloudx events --service api --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cliOpts)
			if err != nil {
				return fmt.Errorf("failed to load configuration: %w", err)
			}

			dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("failed to connect to cluster store at %s: %w", dbPath, err)
			}
			defer store.Close()

			var sinceTime time.Time
			if strings.TrimSpace(sinceStr) != "" {
				dur, err := time.ParseDuration(sinceStr)
				if err != nil {
					return fmt.Errorf("invalid duration for --since '%s' (e.g. 1h, 30m, 24h): %w", sinceStr, err)
				}
				sinceTime = time.Now().UTC().Add(-dur)
			}

			// Gather matching entity IDs if service or node filter is specified
			var serviceEntityIDs map[id.ID]bool
			if strings.TrimSpace(serviceFilter) != "" {
				serviceEntityIDs = make(map[id.ID]bool)
				services, err := store.Services().List(ctx)
				if err == nil {
					for _, s := range services {
						if strings.EqualFold(s.Name, serviceFilter) || s.ID.String() == serviceFilter {
							serviceEntityIDs[s.ID] = true
							// Also match all deployments and tasks under this service
							deps, _ := store.Deployments().ListByService(ctx, s.ID)
							for _, d := range deps {
								serviceEntityIDs[d.ID] = true
							}
							tsks, _ := store.Tasks().ListByService(ctx, s.ID)
							for _, t := range tsks {
								serviceEntityIDs[t.ID] = true
							}
							break
						}
					}
				}
			}

			var nodeEntityIDs map[id.ID]bool
			if strings.TrimSpace(nodeFilter) != "" {
				nodeEntityIDs = make(map[id.ID]bool)
				nodes, _ := store.Nodes().List(ctx)
				for _, n := range nodes {
					if strings.EqualFold(n.Name, nodeFilter) || n.ID.String() == nodeFilter {
						nodeEntityIDs[n.ID] = true
					}
				}
				workers, _ := store.Workers().List(ctx)
				for _, w := range workers {
					if strings.EqualFold(w.Address, nodeFilter) || w.ID.String() == nodeFilter || (w.NodeID != "" && nodeEntityIDs[w.NodeID]) {
						nodeEntityIDs[w.ID] = true
						if w.NodeID != "" {
							nodeEntityIDs[w.NodeID] = true
						}
					}
				}
			}

			recorder := events.NewRecorder(store, logging.NewDefaultLogger())
			fetchLimit := limit
			if fetchLimit <= 0 {
				fetchLimit = 250
			}

			rawEvents, err := recorder.List(ctx, events.EventFilter{
				Type:  typeFilter,
				Since: sinceTime,
				Limit: fetchLimit * 2,
			})
			if err != nil {
				return fmt.Errorf("failed to list cluster events: %w", err)
			}

			// Filter events in memory by service / node entity if requested
			var matchedEvents []*models.Event
			for _, e := range rawEvents {
				if serviceEntityIDs != nil {
					// Check entity ID or payload mentions
					if !serviceEntityIDs[e.EntityID] && !strings.Contains(e.Payload, fmt.Sprintf(`"service":"%s"`, serviceFilter)) && !strings.Contains(e.Payload, fmt.Sprintf(`"service_id":"%s"`, serviceFilter)) {
						continue
					}
				}
				if nodeEntityIDs != nil {
					if !nodeEntityIDs[e.EntityID] && !strings.Contains(e.Payload, nodeFilter) {
						continue
					}
				}
				matchedEvents = append(matchedEvents, e)
			}

			// Sort chronologically ascending (oldest to newest)
			sort.Slice(matchedEvents, func(i, j int) bool {
				return matchedEvents[i].CreatedAt.Before(matchedEvents[j].CreatedAt)
			})

			if len(matchedEvents) > fetchLimit {
				matchedEvents = matchedEvents[len(matchedEvents)-fetchLimit:]
			}

			out := cmd.OutOrStdout()

			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(matchedEvents)
			}

			if len(matchedEvents) == 0 {
				fmt.Fprintln(out, "No cluster events found matching criteria.")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "TIMESTAMP\tTYPE\tSOURCE\tENTITY ID\tDETAILS")
			for _, e := range matchedEvents {
				formattedPayload := formatEventPayload(e.Payload)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					e.CreatedAt.Format(time.RFC3339),
					e.Type,
					e.Source,
					e.EntityID,
					formattedPayload,
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&serviceFilter, "service", "", "Filter events by service name or ID")
	cmd.Flags().StringVar(&nodeFilter, "node", "", "Filter events by node/worker name or ID")
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter events by event type (e.g. WORKER_LOST, SERVICE_CREATED)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "Filter events created within duration (e.g. 10m, 1h, 24h)")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum number of events to display")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output events in JSON format")

	return cmd
}

// formatEventPayload produces a concise string from JSON payload for CLI display.
func formatEventPayload(payload string) string {
	if strings.TrimSpace(payload) == "" {
		return "-"
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return payload
	}

	var parts []string
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}
