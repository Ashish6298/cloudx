package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cloudx-org/cloudx/internal/common/logging"
	"github.com/cloudx-org/cloudx/internal/config"
	"github.com/cloudx-org/cloudx/internal/controlplane"
	"github.com/cloudx-org/cloudx/internal/state/models"
	"github.com/cloudx-org/cloudx/internal/state/sqlite"
	"github.com/spf13/cobra"
)

// newVolumeCmd returns the top-level `cloudx volume` command with subcommands.
func newVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "volume",
		Aliases: []string{"volumes", "vol"},
		Short:   "Manage persistent storage volumes",
		Long: `Create, list, inspect, and delete CloudX persistent volumes.

Volumes provide durable storage for workloads that must retain data across
process crashes and restarts. Local volumes are backed by host filesystem
directories provisioned on volume creation.

Examples:
  cloudx volume create data
  cloudx volume create data --driver local --location /data/myapp
  cloudx volume list
  cloudx volume inspect data
  cloudx volume delete data`,
	}

	cmd.AddCommand(newVolumeCreateCmd())
	cmd.AddCommand(newVolumeListCmd())
	cmd.AddCommand(newVolumeInspectCmd())
	cmd.AddCommand(newVolumeDeleteCmd())
	return cmd
}

// newVolumeCreateCmd returns the `cloudx volume create <name>` command.
func newVolumeCreateCmd() *cobra.Command {
	var driver string
	var location string
	var sizeStr string
	var ownerRef string
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Provision a new persistent volume",
		Long: `Create a named persistent volume backed by a local host directory.

The volume directory is created immediately on provisioning.
Workloads that use the volume will find its path exposed via the
environment variable CLOUDX_VOLUME_<NAME_UPPER>.

Examples:
  cloudx volume create data
  cloudx volume create db-storage --driver local --location /srv/cloudx/db
  cloudx volume create logs --size 2GB`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("volume name cannot be empty")
			}

			cp, store, cleanup, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			defer cleanup()
			_ = store

			drv := models.VolumeDriver(strings.ToLower(driver))
			if drv == "" {
				drv = models.VolumeDriverLocal
			}

			sizeMeta := models.VolumeSizeMetadata{}
			if sizeStr != "" {
				sizeMeta.HumanReadable = sizeStr
			}

			cfg := models.VolumeConfig{
				Name:     name,
				Driver:   drv,
				Location: location,
				Size:     sizeMeta,
				OwnerRef: ownerRef,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			result, err := cp.CreateVolume(ctx, cfg)
			if err != nil {
				return fmt.Errorf("failed to create volume: %w", err)
			}

			if isJSONOutput(cmd, jsonOutput) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			fmt.Fprintf(out, "Volume created successfully.\n\n")
			fmt.Fprintf(out, "  ID:       %s\n", result.VolumeID)
			fmt.Fprintf(out, "  Name:     %s\n", result.VolumeName)
			fmt.Fprintf(out, "  Driver:   %s\n", result.Driver)
			fmt.Fprintf(out, "  Location: %s\n", result.Location)
			fmt.Fprintf(out, "  State:    %s\n", result.State)
			fmt.Fprintf(out, "  Created:  %s\n", result.CreatedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "\nData written to %s will persist across process restarts.\n", result.Location)
			return nil
		},
	}

	cmd.Flags().StringVar(&driver, "driver", "local", "Volume driver (local|host)")
	cmd.Flags().StringVar(&location, "location", "", "Host filesystem path for the volume (auto-generated if empty)")
	cmd.Flags().StringVar(&sizeStr, "size", "", "Volume size hint (e.g. 1GB, 500MB) — informational for local volumes")
	cmd.Flags().StringVar(&ownerRef, "owner", "", "Owning service or job reference")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

// newVolumeListCmd returns the `cloudx volume list` command.
func newVolumeListCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all persistent volumes",
		Long:    `Display all volumes tracked by the CloudX cluster.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			cp, store, cleanup, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			defer cleanup()
			_ = store

			ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
			defer cancel()

			vols, err := cp.ListVolumes(ctx)
			if err != nil {
				return fmt.Errorf("failed to list volumes: %w", err)
			}

			if isJSONOutput(cmd, jsonOutput) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(vols)
			}

			if len(vols) == 0 {
				fmt.Fprintln(out, "No volumes found.")
				return nil
			}

			tw := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(tw, "NAME\tID\tDRIVER\tSTATE\tLOCATION\tCREATED")
			for _, v := range vols {
				createdAgo := formatAgo(v.CreatedAt)
				loc := v.Location
				if len(loc) > 40 {
					loc = "..." + loc[len(loc)-37:]
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					v.Name,
					v.ID,
					v.Driver,
					v.State,
					loc,
					createdAgo,
				)
			}
			return tw.Flush()
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

// newVolumeInspectCmd returns the `cloudx volume inspect <name|id>` command.
func newVolumeInspectCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "inspect <name|id>",
		Short: "Show detailed information about a volume",
		Long: `Display complete configuration, state, and filesystem status for a volume.

Examples:
  cloudx volume inspect data
  cloudx volume inspect vol-000001a2b3c4-aabbccddeeff`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			nameOrID := args[0]

			cp, store, cleanup, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			defer cleanup()
			_ = store

			ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
			defer cancel()

			result, err := cp.InspectVolume(ctx, nameOrID)
			if err != nil {
				return fmt.Errorf("failed to inspect volume: %w", err)
			}

			if isJSONOutput(cmd, jsonOutput) {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			v := result.Volume
			fmt.Fprintf(out, "VOLUME: %s\n", v.Name)
			fmt.Fprintf(out, "  ID:            %s\n", v.ID)
			fmt.Fprintf(out, "  Driver:        %s\n", v.Driver)
			fmt.Fprintf(out, "  State:         %s\n", v.State)
			fmt.Fprintf(out, "  Location:      %s\n", v.Location)
			fmt.Fprintf(out, "  Dir Present:   %v\n", result.DirExists)
			if result.DirSizeMsg != "" {
				fmt.Fprintf(out, "  Dir Status:    %s\n", result.DirSizeMsg)
			}
			if v.OwnerRef != "" {
				fmt.Fprintf(out, "  Owner Ref:     %s\n", v.OwnerRef)
			}
			if v.WorkerID != "" {
				fmt.Fprintf(out, "  Worker ID:     %s\n", v.WorkerID)
			}
			if v.Size.HumanReadable != "" {
				fmt.Fprintf(out, "  Size:          %s\n", v.Size.HumanReadable)
			}
			fmt.Fprintf(out, "  Config Hash:   %s\n", v.ConfigHash)
			fmt.Fprintf(out, "  Created:       %s\n", v.CreatedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "  Updated:       %s\n", v.UpdatedAt.Format(time.RFC3339))
			fmt.Fprintf(out, "\nProcess ENV key: CLOUDX_VOLUME_%s=%s\n",
				strings.ToUpper(strings.ReplaceAll(v.Name, "-", "_")),
				v.Location,
			)
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	return cmd
}

// newVolumeDeleteCmd returns the `cloudx volume delete <name|id>` command.
func newVolumeDeleteCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "delete <name|id>",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a persistent volume",
		Long: `Remove a CloudX volume record and attempt to clean up the host directory.

The directory is removed only if it is empty. Non-empty directories are left in
place to prevent data loss. The state record is always removed.

Examples:
  cloudx volume delete data
  cloudx volume delete vol-000001a2b3c4-aabbccddeeff`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			nameOrID := args[0]

			cp, store, cleanup, err := newControlPlaneClient()
			if err != nil {
				return err
			}
			defer cleanup()
			_ = store
			_ = force

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := cp.DeleteVolume(ctx, nameOrID); err != nil {
				return fmt.Errorf("failed to delete volume: %w", err)
			}

			fmt.Fprintf(out, "Volume '%s' deleted successfully.\n", nameOrID)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force deletion even if directory is non-empty")
	return cmd
}

// newControlPlaneClient creates a minimal, embedded control plane client for CLI use.
// It opens the local SQLite store and returns a ControlPlane along with a cleanup function.
func newControlPlaneClient() (*controlplane.ControlPlane, interface{}, func(), error) {
	cfg, err := config.Load(cliOpts)
	if err != nil {
		return nil, nil, func() {}, fmt.Errorf("failed to load configuration: %w", err)
	}

	logger := logging.New(logging.ParseLevel(cfg.Logging.Level), logging.FormatText)

	dbPath := filepath.Join(cfg.Storage.Path, "cloudx.db")
	ctx := context.Background()
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, func() {}, fmt.Errorf("failed to open state store: %w", err)
	}

	cp, err := controlplane.New(controlplane.Options{
		Config: cfg,
		Store:  store,
		Logger: logger,
	})
	if err != nil {
		_ = store.Close()
		return nil, nil, func() {}, fmt.Errorf("failed to initialize control plane: %w", err)
	}

	cleanup := func() {
		_ = store.Close()
	}
	return cp, store, cleanup, nil
}

// formatAgo formats a timestamp as a human-readable duration relative to now.
func formatAgo(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("2006-01-02")
	}
}
