package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/spf13/cobra"
)

// newNetworkCmd returns the top-level `cloudx network` command with subcommands.
func newNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "network",
		Aliases: []string{"networks", "net"},
		Short:   "Manage logical CloudX networks",
		Long: `Create, list, inspect, and delete logical CloudX networks.

Logical networks group related services together and enable logical service
discovery and peer referencing across workloads without physical overlay complexity.

Examples:
  cloudx network create backend
  cloudx network create frontend --subnet 10.244.1.0/24
  cloudx network list
  cloudx network inspect backend
  cloudx network delete backend`,
	}

	cmd.AddCommand(newNetworkCreateCmd())
	cmd.AddCommand(newNetworkListCmd())
	cmd.AddCommand(newNetworkInspectCmd())
	cmd.AddCommand(newNetworkDeleteCmd())
	return cmd
}

// newNetworkCreateCmd returns the `cloudx network create <name>` command.
func newNetworkCreateCmd() *cobra.Command {
	var subnet string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a logical network",
		Long: `Create a named logical network for service grouping and peer discovery.

Examples:
  cloudx network create backend
  cloudx network create frontend --subnet 10.244.1.0/24`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("network name cannot be empty")
			}

			cp, _, cleanup, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			res, err := cp.CreateNetwork(ctx, controlplane.NetworkCreateOptions{
				Name:   name,
				Subnet: subnet,
			})
			if err != nil {
				return fmt.Errorf("failed to create network: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, "Network '%s' created successfully!\n\n", res.NetworkName)
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NETWORK ID\tNAME\tSUBNET\tCREATED")
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				res.NetworkID,
				res.NetworkName,
				res.Subnet,
				res.CreatedAt.Format(time.RFC3339),
			)
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&subnet, "subnet", "", "Subnet CIDR for the logical network (default: 10.244.0.0/16)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

// newNetworkListCmd returns the `cloudx network list` command.
func newNetworkListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all logical networks",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cp, _, cleanup, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			nets, err := cp.ListNetworks(ctx)
			if err != nil {
				return fmt.Errorf("failed to list networks: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(nets)
			}

			if len(nets) == 0 {
				fmt.Fprintln(out, "No logical networks found in cluster. Use 'cloudx network create <name>' to create one.")
				return nil
			}

			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NETWORK ID\tNAME\tSUBNET\tCREATED")
			for _, n := range nets {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					n.ID,
					n.Name,
					n.Subnet,
					formatAgo(n.CreatedAt),
				)
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output results as JSON")
	return cmd
}

// newNetworkInspectCmd returns the `cloudx network inspect <name|id>` command.
func newNetworkInspectCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "inspect <name | id>",
		Short: "Show detailed information about a logical network and its members",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			nameOrID := strings.TrimSpace(args[0])

			cp, _, cleanup, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			res, err := cp.InspectNetwork(ctx, nameOrID)
			if err != nil {
				return fmt.Errorf("failed to inspect network: %w", err)
			}

			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			fmt.Fprintf(out, "Network: %s\n", res.Network.Name)
			fmt.Fprintf(out, "ID:      %s\n", res.Network.ID)
			fmt.Fprintf(out, "Subnet:  %s\n", res.Network.Subnet)
			fmt.Fprintf(out, "Created: %s (%s)\n\n", res.Network.CreatedAt.Format(time.RFC3339), formatAgo(res.Network.CreatedAt))

			if len(res.Services) == 0 {
				fmt.Fprintln(out, "Member Services: (none)")
			} else {
				fmt.Fprintf(out, "Member Services (%d):\n", len(res.Services))
				for _, s := range res.Services {
					fmt.Fprintf(out, "  - %s\n", s)
				}
			}

			fmt.Println()
			if len(res.Endpoints) == 0 {
				fmt.Fprintln(out, "Active Endpoints: (none)")
			} else {
				fmt.Fprintf(out, "Active Endpoints (%d):\n", len(res.Endpoints))
				w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
				fmt.Fprintln(w, "  SERVICE\tTASK ID\tENDPOINT\tPROTOCOL\tHEALTHY")
				for _, ep := range res.Endpoints {
					healthyStr := "true"
					if !ep.Healthy {
						healthyStr = "false"
					}
					fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\n",
						ep.ServiceName,
						ep.TaskID,
						ep.Address,
						ep.Protocol,
						healthyStr,
					)
				}
				_ = w.Flush()
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output inspection details as JSON")
	return cmd
}

// newNetworkDeleteCmd returns the `cloudx network delete <name|id>` command.
func newNetworkDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <name | id>",
		Aliases: []string{"rm"},
		Short:   "Delete a logical network",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			nameOrID := strings.TrimSpace(args[0])

			cp, _, cleanup, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			defer cleanup()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := cp.DeleteNetwork(ctx, nameOrID); err != nil {
				return fmt.Errorf("failed to delete network: %w", err)
			}

			fmt.Fprintf(out, "Network '%s' deleted successfully.\n", nameOrID)
			return nil
		},
	}

	return cmd
}
