package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	datasetsCmd.AddCommand(datasetsLsCmd)
	datasetsCmd.AddCommand(datasetsCreateCmd)
	datasetsCmd.PersistentFlags().StringP("project", "p", "", "Project ID (defaults to first project)")
	rootCmd.AddCommand(datasetsCmd)
}

var datasetsCmd = &cobra.Command{
	Use:   "datasets",
	Short: "Manage catalog datasets",
}

func projectQuery(cmd *cobra.Command) string {
	if p, _ := cmd.Flags().GetString("project"); p != "" {
		return "?project=" + p
	}
	return ""
}

var datasetsLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List datasets",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/datasets"+projectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Datasets []struct {
				Name string `json:"name"`
			} `json:"datasets"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		for _, d := range out.Datasets {
			fmt.Println(d.Name)
		}
		return nil
	},
}

var datasetsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]string{"name": args[0]})
		data, err := authedPost(cfg, "/datasets"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &out)
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("dataset %q created\n", args[0])
		return nil
	},
}
