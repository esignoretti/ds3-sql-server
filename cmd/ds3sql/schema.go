package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	schemaCmd.Flags().StringP("project", "p", "", "Project ID (defaults to first project)")
	rootCmd.AddCommand(schemaCmd)
}

var schemaCmd = &cobra.Command{
	Use:   "schema <s3-path>",
	Short: "Show columns and types for a S3 path",
	Args:  cobra.ExactArgs(1),
	Long: `Show schema for Parquet/CSV files at an S3 path.
Example: ds3sql schema 's3://my-bucket/logs/*.parquet'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		body, _ := json.Marshal(map[string]string{"path": args[0]})

		path := "/schema"
		if p, _ := cmd.Flags().GetString("project"); p != "" {
			path += "?project=" + p
		}

		data, err := authedPost(cfg, path, body)
		if err != nil {
			return err
		}

		var result struct {
			Columns []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Nullable bool   `json:"nullable"`
			} `json:"columns"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		if result.Error != "" {
			return fmt.Errorf("schema error: %s", result.Error)
		}

		if len(result.Columns) == 0 {
			fmt.Println("No columns found")
			return nil
		}

		fmt.Printf("%-30s %-20s %s\n", "COLUMN", "TYPE", "NULLABLE")
		fmt.Println("----------------------------------------------")
		for _, c := range result.Columns {
			nullable := "NO"
			if c.Nullable {
				nullable = "YES"
			}
			fmt.Printf("%-30s %-20s %s\n", c.Name, c.Type, nullable)
		}
		return nil
	},
}
