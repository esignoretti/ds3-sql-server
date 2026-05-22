package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	bucketsCmd.Flags().StringP("project", "p", "", "Project ID (defaults to first project)")
	rootCmd.AddCommand(bucketsCmd)
}

var bucketsCmd = &cobra.Command{
	Use:   "buckets",
	Short: "List accessible S3 buckets",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		path := "/buckets"
		if p, _ := cmd.Flags().GetString("project"); p != "" {
			path += "?project=" + p
		}

		data, err := authedGet(cfg, path)
		if err != nil {
			return err
		}

		var result struct {
			Buckets []struct {
				Name         string `json:"name"`
				CreationDate string `json:"creation_date"`
			} `json:"buckets"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if result.Error != "" {
			return fmt.Errorf("server error: %s", result.Error)
		}

		if len(result.Buckets) == 0 {
			fmt.Println("No buckets")
			return nil
		}

		fmt.Printf("%-30s %s\n", "NAME", "CREATED")
		fmt.Println("----------------------------------------------")
		for _, b := range result.Buckets {
			fmt.Printf("%-30s %s\n", b.Name, b.CreationDate)
		}
		return nil
	},
}
