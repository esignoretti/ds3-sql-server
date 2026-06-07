package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func init() {
	schedulesCmd.AddCommand(schedulesCreateCmd)
	schedulesCmd.AddCommand(schedulesLsCmd)
	schedulesCmd.AddCommand(schedulesRmCmd)
	schedulesCmd.PersistentFlags().StringP("project", "p", "", "Project ID (defaults to first project)")
	schedulesCreateCmd.Flags().String("cron", "", "Cron expression, e.g. \"0 * * * *\" (required)")
	schedulesCreateCmd.Flags().String("sql", "", "SQL to run (required)")
	schedulesCreateCmd.Flags().String("into", "", "Optional target managed table dataset.table")
	rootCmd.AddCommand(schedulesCmd)
}

var schedulesCmd = &cobra.Command{
	Use:   "schedules",
	Short: "Manage scheduled queries",
}

var schedulesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a scheduled query",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		cron, _ := cmd.Flags().GetString("cron")
		sql, _ := cmd.Flags().GetString("sql")
		into, _ := cmd.Flags().GetString("into")
		if cron == "" || sql == "" {
			return fmt.Errorf("--cron and --sql are required")
		}
		body, _ := json.Marshal(map[string]string{"cron": cron, "sql": sql, "into_table": into})
		data, err := authedPost(cfg, "/schedules"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			ID    string `json:"id"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("schedule %s created\n", out.ID)
		return nil
	},
}

var schedulesLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List scheduled queries",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/schedules"+projectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Schedules []struct {
				ID        string `json:"id"`
				Cron      string `json:"cron"`
				IntoTable string `json:"into_table"`
				NextRunAt string `json:"next_run_at"`
			} `json:"schedules"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "ID\tCRON\tINTO\tNEXT_RUN")
		for _, s := range out.Schedules {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", s.ID, s.Cron, s.IntoTable, s.NextRunAt)
		}
		w.Flush()
		return nil
	},
}

var schedulesRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Delete a scheduled query",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if err := authedDelete(cfg, "/schedules/"+args[0]+projectQuery(cmd)); err != nil {
			return err
		}
		fmt.Printf("schedule %s deleted\n", args[0])
		return nil
	},
}
