package main

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

var (
	// globalOutputFormat holds the format passed via persistent --output / -o flag
	globalOutputFormat string
)

// isJSONOutput determines whether JSON output format is requested, either via
// explicit local command flag (e.g. --json or --output json) or the persistent
// global flag (--output=json / -o json / --json).
func isJSONOutput(cmd *cobra.Command, localJSONFlag bool) bool {
	if localJSONFlag {
		return true
	}

	// Check persistent global output flag
	if strings.EqualFold(strings.TrimSpace(globalOutputFormat), "json") {
		return true
	}

	// Check command-level flags if registered
	if cmd != nil {
		if f := cmd.Flags().Lookup("output"); f != nil && strings.EqualFold(strings.TrimSpace(f.Value.String()), "json") {
			return true
		}
		if f := cmd.Flags().Lookup("json"); f != nil && f.Value.String() == "true" {
			return true
		}
		if f := cmd.InheritedFlags().Lookup("output"); f != nil && strings.EqualFold(strings.TrimSpace(f.Value.String()), "json") {
			return true
		}
	}

	return false
}

// writeJSON encodes the value as indented JSON to the provided writer.
func writeJSON(w io.Writer, val any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(val)
}
