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
	queryCmd.Flags().Bool("json", false, "Output as JSON")
	queryCmd.Flags().StringP("file", "f", "", "Read SQL from file")
	queryCmd.Flags().StringP("project", "p", "", "Project ID (defaults to first project)")
	rootCmd.AddCommand(queryCmd)
}

var queryCmd = &cobra.Command{
	Use:   "query <sql>",
	Short: "Execute SQL query against S3 data",
	Args:  cobra.RangeArgs(0, 1),
	Long: `Execute a SQL query against data in S3.
Examples:
  ds3sql query "SELECT count(*) FROM read_parquet('s3://bucket/*.parquet')"
  ds3sql query -f query.sql`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		var sql string
		if file, _ := cmd.Flags().GetString("file"); file != "" {
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			sql = string(data)
		} else if len(args) > 0 {
			sql = args[0]
		} else {
			return fmt.Errorf("provide SQL as argument or with --file")
		}

		body, _ := json.Marshal(map[string]string{"sql": sql})

		path := "/query"
		if p, _ := cmd.Flags().GetString("project"); p != "" {
			path += "?project=" + p
		}
		data, err := authedPost(cfg, path, body)
		if err != nil {
			return err
		}

		var result struct {
			Columns   []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"columns"`
			Rows      [][]any `json:"rows"`
			RowCount  int     `json:"row_count"`
			ElapsedMs int64   `json:"elapsed_ms"`
			Error     string  `json:"error"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		if result.Error != "" {
			return fmt.Errorf("query error: %s", result.Error)
		}

		if isJSON, _ := cmd.Flags().GetBool("json"); isJSON {
			fmt.Println(string(data))
			return nil
		}

		if result.RowCount == 0 {
			fmt.Println("No rows returned")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

		headers := make([]string, len(result.Columns))
		for i, c := range result.Columns {
			headers[i] = c.Name
		}
		fmt.Fprintln(w, strings.Join(headers, "\t"))

		seps := make([]string, len(result.Columns))
		for i := range seps {
			seps[i] = "---"
		}
		fmt.Fprintln(w, strings.Join(seps, "\t"))

		for _, row := range result.Rows {
			cells := make([]string, len(row))
			for i, cell := range row {
				if cell == nil {
					cells[i] = "NULL"
				} else {
					cells[i] = fmt.Sprintf("%v", cell)
				}
			}
			fmt.Fprintln(w, strings.Join(cells, "\t"))
		}
		w.Flush()

		fmt.Printf("\n(%d rows, %dms)\n", result.RowCount, result.ElapsedMs)
		return nil
	},
}
