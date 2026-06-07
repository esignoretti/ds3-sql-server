package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func init() {
	tablesCmd.AddCommand(tablesLsCmd)
	tablesCmd.AddCommand(tablesRegisterCmd)
	tablesCmd.AddCommand(tablesDescribeCmd)
	tablesCmd.AddCommand(tablesDropCmd)
	tablesCmd.AddCommand(tablesCreateAsCmd)
	tablesCmd.PersistentFlags().StringP("project", "p", "", "Project ID (defaults to first project)")
	tablesRegisterCmd.Flags().String("location", "", "S3 location or glob (required)")
	tablesRegisterCmd.Flags().String("format", "parquet", "File format: parquet | csv | tsv | json")
	tablesRegisterCmd.Flags().String("storage-class", "hdd", "Storage class: ssd | hdd")
	tablesRegisterCmd.Flags().StringSlice("partition-by", nil, "Partition columns")
	tablesCreateAsCmd.Flags().String("as", "", "Inner SELECT statement (required)")
	tablesCreateAsCmd.Flags().StringSlice("partition-by", nil, "Partition columns")
	tablesCreateAsCmd.Flags().String("storage-class", "", "Storage class: ssd | hdd")
	rootCmd.AddCommand(tablesCmd)
}

var tablesCmd = &cobra.Command{
	Use:   "tables",
	Short: "Manage catalog tables",
}

// splitRef splits "dataset.table" into its parts.
func splitRef(ref string) (string, string, error) {
	parts := strings.SplitN(ref, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected dataset.table, got %q", ref)
	}
	return parts[0], parts[1], nil
}

var tablesLsCmd = &cobra.Command{
	Use:   "ls <dataset>",
	Short: "List tables in a dataset",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/datasets/"+args[0]+"/tables"+projectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Tables []struct {
				Name   string `json:"name"`
				Format string `json:"format"`
			} `json:"tables"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		for _, t := range out.Tables {
			fmt.Printf("%s\t%s\n", t.Name, t.Format)
		}
		return nil
	},
}

var tablesRegisterCmd = &cobra.Command{
	Use:   "register <dataset.table>",
	Short: "Register an external table over existing S3 data",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		dataset, name, err := splitRef(args[0])
		if err != nil {
			return err
		}
		location, _ := cmd.Flags().GetString("location")
		if location == "" {
			return fmt.Errorf("--location is required")
		}
		format, _ := cmd.Flags().GetString("format")
		storageClass, _ := cmd.Flags().GetString("storage-class")
		partitionBy, _ := cmd.Flags().GetStringSlice("partition-by")
		body, _ := json.Marshal(map[string]any{
			"name":              name,
			"location":          location,
			"format":            format,
			"storage_class":     storageClass,
			"partition_columns": partitionBy,
		})
		data, err := authedPost(cfg, "/datasets/"+dataset+"/tables"+projectQuery(cmd), body)
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
		fmt.Printf("table %s.%s registered\n", dataset, name)
		return nil
	},
}

var tablesDescribeCmd = &cobra.Command{
	Use:   "describe <dataset.table>",
	Short: "Show a table's schema and stats",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		dataset, name, err := splitRef(args[0])
		if err != nil {
			return err
		}
		data, err := authedGet(cfg, "/datasets/"+dataset+"/tables/"+name+projectQuery(cmd))
		if err != nil {
			return err
		}
		var out struct {
			Schema []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"schema"`
			Stats struct {
				RowCount int64 `json:"row_count"`
			} `json:"stats"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "COLUMN\tTYPE")
		for _, c := range out.Schema {
			fmt.Fprintf(w, "%s\t%s\n", c.Name, c.Type)
		}
		w.Flush()
		fmt.Printf("\nrows: %d\n", out.Stats.RowCount)
		return nil
	},
}

var tablesDropCmd = &cobra.Command{
	Use:   "drop <dataset.table>",
	Short: "Drop a table registration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		dataset, name, err := splitRef(args[0])
		if err != nil {
			return err
		}
		if err := authedDelete(cfg, "/datasets/"+dataset+"/tables/"+name+projectQuery(cmd)); err != nil {
			return err
		}
		fmt.Printf("table %s.%s dropped\n", dataset, name)
		return nil
	},
}

var tablesCreateAsCmd = &cobra.Command{
	Use:   "create-as <dataset.table>",
	Short: "Create a managed table from a SELECT (CTAS)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		dataset, name, err := splitRef(args[0])
		if err != nil {
			return err
		}
		sel, _ := cmd.Flags().GetString("as")
		if sel == "" {
			return fmt.Errorf("--as \"SELECT ...\" is required")
		}
		partitionBy, _ := cmd.Flags().GetStringSlice("partition-by")
		storageClass, _ := cmd.Flags().GetString("storage-class")

		var sb strings.Builder
		fmt.Fprintf(&sb, "CREATE TABLE %s.%s", dataset, name)
		if len(partitionBy) > 0 {
			fmt.Fprintf(&sb, " PARTITION BY (%s)", strings.Join(partitionBy, ", "))
		}
		if storageClass != "" {
			fmt.Fprintf(&sb, " STORAGE '%s'", storageClass)
		}
		fmt.Fprintf(&sb, " AS %s", sel)

		body, _ := json.Marshal(map[string]string{"sql": sb.String()})
		data, err := authedPost(cfg, "/jobs"+projectQuery(cmd), body)
		if err != nil {
			return err
		}
		var out struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			IntoTable string `json:"into_table"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		fmt.Printf("ctas job %s submitted (%s)\n", out.ID, out.Status)
		return nil
	},
}
