package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
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

		data, err := authedGet(cfg, "/buckets")
		if err != nil {
			return err
		}

		var result struct {
			Buckets []struct {
				Name         string `json:"name"`
				CreationDate string `json:"creation_date"`
			} `json:"buckets"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		fmt.Printf("%-30s %s\n", "NAME", "CREATED")
		fmt.Println("----------------------------------------------")
		for _, b := range result.Buckets {
			fmt.Printf("%-30s %s\n", b.Name, b.CreationDate)
		}
		return nil
	},
}
