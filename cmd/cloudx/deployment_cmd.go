package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

func newDeploymentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deployment",
		Aliases: []string{"deployments", "dep"},
		Short:   "Inspect and manage immutable deployment records",
		Long: `Inspect immutable deployment records and history across services in the CloudX cluster.
Each deployment is uniquely identifiable by service, version, timestamp, configuration hash, and status.`,
	}

	cmd.AddCommand(newDeploymentListCmd())
	cmd.AddCommand(newDeploymentInspectCmd())
	return cmd
}

func newDeploymentListCmd() *cobra.Command {
	var jsonOutput bool
	var serviceName string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List immutable deployments across services",
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

			var allDeployments []*models.ImmutableDeployment
			for _, svc := range services {
				if serviceName != "" && svc.Name != serviceName && svc.ID.String() != serviceName {
					continue
				}

				deps, err := store.Deployments().ListByService(ctx, svc.ID)
				if err != nil {
					continue
				}

				for _, d := range deps {
					imm, err := models.DeploymentFromModel(d)
					if err == nil {
						if imm.ServiceName == "" {
							imm.ServiceName = svc.Name
						}
						allDeployments = append(allDeployments, imm)
					}
				}
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(allDeployments)
			}

			if len(allDeployments) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No deployments found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "DEPLOYMENT ID\tSERVICE\tVERSION\tREPLICAS\tSTATUS\tCONFIG HASH\tCREATED")
			for _, d := range allDeployments {
				shortHash := d.ConfigHash
				if len(shortHash) > 8 {
					shortHash = shortHash[:8]
				}
				if shortHash == "" {
					shortHash = "-"
				}

				replicas := d.Config.Replicas
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
					d.ID,
					d.ServiceName,
					d.Version,
					replicas,
					d.Status,
					shortHash,
					d.CreatedAt.Format(time.RFC3339),
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output deployment list as JSON")
	cmd.Flags().StringVarP(&serviceName, "service", "s", "", "Filter deployments by service name or ID")
	return cmd
}

func newDeploymentInspectCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "inspect <deployment-id>",
		Short: "Inspect an immutable deployment record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			depID := args[0]

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

			// Search deployment across services
			services, err := store.Services().List(ctx)
			if err != nil {
				return fmt.Errorf("failed to list services: %w", err)
			}

			var targetDeployment *models.ImmutableDeployment
			for _, svc := range services {
				deps, err := store.Deployments().ListByService(ctx, svc.ID)
				if err != nil {
					continue
				}
				for _, d := range deps {
					imm, err := models.DeploymentFromModel(d)
					if err == nil {
						if imm.ServiceName == "" {
							imm.ServiceName = svc.Name
						}
						// Match by exact deployment ID or service:version
						if d.ID.String() == depID || fmt.Sprintf("%s:%s", svc.Name, imm.Version) == depID || (svc.Name == depID && imm.Status == models.DeploymentStatusActive) {
							targetDeployment = imm
							break
						}
					}
				}
				if targetDeployment != nil {
					break
				}
			}

			if targetDeployment == nil {
				return fmt.Errorf("deployment '%s' not found", depID)
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(targetDeployment)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "DEPLOYMENT: %s\n", targetDeployment.ID)
			fmt.Fprintf(out, "  Service:      %s (%s)\n", targetDeployment.ServiceName, targetDeployment.ServiceID)
			fmt.Fprintf(out, "  Version:      %s\n", targetDeployment.Version)
			fmt.Fprintf(out, "  Status:       %s\n", targetDeployment.Status)
			fmt.Fprintf(out, "  Config Hash:  %s\n", targetDeployment.ConfigHash)
			fmt.Fprintf(out, "  Created:      %s\n", targetDeployment.CreatedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "  Updated:      %s\n\n", targetDeployment.UpdatedAt.Format(time.RFC3339))

			fmt.Fprintln(out, "CONFIGURATION SNAPSHOT:")
			fmt.Fprintf(out, "  Command:      %s\n", targetDeployment.Config.Command)
			if targetDeployment.Config.Artifact != "" {
				fmt.Fprintf(out, "  Artifact:     %s\n", targetDeployment.Config.Artifact)
			}
			fmt.Fprintf(out, "  Runtime:      %s\n", targetDeployment.Config.Runtime)
			fmt.Fprintf(out, "  Replicas:     %d\n", targetDeployment.Config.Replicas)
			fmt.Fprintf(out, "  CPU:          %.2f cores\n", targetDeployment.Config.Resources.CPU)
			fmt.Fprintf(out, "  Memory:       %d bytes\n", targetDeployment.Config.Resources.Memory)
			fmt.Fprintf(out, "  Restart:      %s\n", targetDeployment.Config.RestartPolicy.Type)

			if len(targetDeployment.Config.Environment) > 0 {
				fmt.Fprintln(out, "  Environment:")
				for k, v := range targetDeployment.Config.Environment {
					fmt.Fprintf(out, "    %s: %s\n", k, v)
				}
			}

			if len(targetDeployment.Config.Ports) > 0 {
				fmt.Fprintln(out, "  Ports:")
				for _, p := range targetDeployment.Config.Ports {
					fmt.Fprintf(out, "    - Host: %d -> Service: %d (%s)\n", p.HostPort, p.ServicePort, p.Protocol)
				}
			}

			if len(targetDeployment.Config.Volumes) > 0 {
				fmt.Fprintln(out, "  Volumes:")
				for _, v := range targetDeployment.Config.Volumes {
					fmt.Fprintf(out, "    - Mount: %s (Read-Only: %v)\n", v.MountPath, v.ReadOnly)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output deployment inspection as JSON")
	return cmd
}
