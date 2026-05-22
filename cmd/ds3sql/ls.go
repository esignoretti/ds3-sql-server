package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	lsCmd.Flags().StringP("prefix", "p", "", "Prefix to filter by")
	rootCmd.AddCommand(lsCmd)
}

var lsCmd = &cobra.Command{
	Use:   "ls <bucket>",
	Short: "List objects in a bucket",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		bucket := args[0]
		prefix, _ := cmd.Flags().GetString("prefix")

		url := fmt.Sprintf("/buckets/%s", bucket)
		if prefix != "" {
			url += "?prefix=" + prefix
		}

		data, err := authedGet(cfg, url)
		if err != nil {
			return err
		}

		var result struct {
			Prefixes    []string `json:"prefixes"`
			Objects     []struct {
				Key          string `json:"key"`
				Size         int64  `json:"size"`
				LastModified string `json:"last_modified"`
			} `json:"objects"`
			IsTruncated bool `json:"is_truncated"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		for _, p := range result.Prefixes {
			fmt.Printf("📁 %-50s\n", strings.TrimSuffix(p, "/")+"/")
		}
		for _, o := range result.Objects {
			size := formatBytes(o.Size)
			fmt.Printf("📄 %-50s %10s  %s\n", o.Key, size, o.LastModified)
		}
		if result.IsTruncated {
			fmt.Println("... (truncated, use --prefix to narrow)")
		}
		return nil
	},
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
