package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(projectsCmd)
}

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List accessible projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		data, err := authedGet(cfg, "/auth/me")
		if err != nil {
			return err
		}

		var result struct {
			Email    string `json:"email"`
			Projects []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"projects"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		fmt.Printf("User: %s\n\n", result.Email)
		fmt.Printf("%-40s %s\n", "PROJECT ID", "NAME")
		fmt.Println("----------------------------------------------")
		for _, p := range result.Projects {
			fmt.Printf("%-40s %s\n", p.ID, p.Name)
		}
		return nil
	},
}
