package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	loadCmd.Flags().String("source", "", "Source glob, e.g. s3://bucket/*.csv (required)")
	loadCmd.Flags().String("into", "", "Target managed table dataset.table (required)")
	loadCmd.Flags().String("format", "csv", "Source format: csv | tsv | json | parquet")
	loadCmd.Flags().StringSlice("partition-by", nil, "Partition columns")
	loadCmd.Flags().String("mode", "append", "append | overwrite")
	loadCmd.Flags().StringP("project", "p", "", "Project ID (defaults to first project)")
	rootCmd.AddCommand(loadCmd)
}

var loadCmd = &cobra.Command{
	Use:   "load",
	Short: "Batch-load data into a managed table",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		source, _ := cmd.Flags().GetString("source")
		into, _ := cmd.Flags().GetString("into")
		if source == "" || into == "" {
			return fmt.Errorf("--source and --into are required")
		}
		format, _ := cmd.Flags().GetString("format")
		mode, _ := cmd.Flags().GetString("mode")
		partitionBy, _ := cmd.Flags().GetStringSlice("partition-by")

		body, _ := json.Marshal(map[string]any{
			"type":         "load",
			"source":       source,
			"into":         into,
			"format":       format,
			"mode":         mode,
			"partition_by": partitionBy,
		})
		data, err := authedPost(cfg, "/jobs"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("load job %s submitted (%s)\n", out.ID, out.Status)
		return nil
	},
}
