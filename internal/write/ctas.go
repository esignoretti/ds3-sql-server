package write

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// CTASPlan is the parsed form of a CREATE TABLE ... AS SELECT statement.
type CTASPlan struct {
	Dataset      string
	Table        string
	PartitionBy  []string
	StorageClass string // "", "ssd", or "hdd"
	Select       string // the inner SELECT, verbatim
}

var (
	// Matches the full documented CTAS grammar. The (?is) flags make it
	// case-insensitive and let . span newlines for the trailing SELECT.
	ctasRe = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
		`([a-zA-Z_][a-zA-Z0-9_]*)\.([a-zA-Z_][a-zA-Z0-9_]*)` +
		`(?:\s+PARTITION\s+BY\s*\(([^)]*)\))?` +
		`(?:\s+STORAGE\s+'([^']*)')?` +
		`\s+AS\s+(.*\S)\s*$`)

	// Quick prefix probe: "CREATE TABLE ... AS" with an AS SELECT somewhere.
	ctasProbeRe = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+.+\s+AS\s+SELECT\b`)
)

// IsCTAS reports whether sql looks like a CREATE TABLE ... AS SELECT statement.
func IsCTAS(sql string) bool {
	return ctasProbeRe.MatchString(sql)
}

// ParseCTAS parses the documented CTAS grammar. It is deliberately strict: any
// statement outside the grammar is rejected with a descriptive error. It is NOT
// a general SQL parser.
func ParseCTAS(sql string) (CTASPlan, error) {
	m := ctasRe.FindStringSubmatch(sql)
	if m == nil {
		return CTASPlan{}, fmt.Errorf("unsupported CTAS form: expected CREATE TABLE [IF NOT EXISTS] <dataset>.<table> [PARTITION BY (cols)] [STORAGE 'ssd'|'hdd'] AS SELECT ...")
	}
	plan := CTASPlan{
		Dataset: m[1],
		Table:   m[2],
		Select:  strings.TrimSpace(m[5]),
	}
	if m[3] != "" {
		for _, c := range strings.Split(m[3], ",") {
			c = strings.TrimSpace(c)
			if c == "" {
				return CTASPlan{}, fmt.Errorf("empty partition column in PARTITION BY")
			}
			plan.PartitionBy = append(plan.PartitionBy, c)
		}
	}
	if m[4] != "" {
		sc := strings.ToLower(strings.TrimSpace(m[4]))
		if sc != "ssd" && sc != "hdd" {
			return CTASPlan{}, fmt.Errorf("invalid storage class %q: must be 'ssd' or 'hdd'", m[4])
		}
		plan.StorageClass = sc
	}
	if !strings.HasPrefix(strings.ToUpper(plan.Select), "SELECT") {
		return CTASPlan{}, fmt.Errorf("CTAS inner statement must be a SELECT")
	}
	return plan, nil
}

// RunCTAS parses, executes, and registers a CREATE TABLE ... AS SELECT.
func (w *Writer) RunCTAS(ctx context.Context, projectID, sql, accessKey, secretKey, endpoint string) (*metastore.Table, error) {
	plan, err := ParseCTAS(sql)
	if err != nil {
		return nil, err
	}
	storageClass := plan.StorageClass
	if storageClass == "" {
		storageClass = "ssd" // managed default tier
	}
	bucket := ""
	if w.storage != nil {
		b, _, ok := w.storage.Resolve(storageClass)
		if !ok {
			return nil, fmt.Errorf("unknown storage class %q", storageClass)
		}
		bucket = b
	}
	location := w.managedLocation(bucket, plan.Dataset, plan.Table)

	// Resolve source catalog tables referenced by the inner SELECT.
	bindings, err := w.cat.Resolve(ctx, projectID, plan.Select)
	if err != nil {
		return nil, fmt.Errorf("resolve sources: %w", err)
	}

	// Ensure the parent directory exists for local paths so DuckDB can create
	// the leaf directory and write files into it.
	if !strings.HasPrefix(location, "s3://") {
		if err := os.MkdirAll(filepath.Dir(location), 0755); err != nil {
			return nil, fmt.Errorf("create target parent dir: %w", err)
		}
	}

	// Build COPY (<select>) TO '<location>' (FORMAT PARQUET [, PARTITION_BY (...)]).
	copyOpts := "FORMAT PARQUET"
	if len(plan.PartitionBy) > 0 {
		quoted := make([]string, len(plan.PartitionBy))
		for i, c := range plan.PartitionBy {
			quoted[i] = c
		}
		copyOpts += ", PARTITION_BY (" + strings.Join(quoted, ", ") + "), OVERWRITE_OR_IGNORE"
	}
	copySQL := fmt.Sprintf("COPY (%s) TO '%s' (%s)", plan.Select, escapeLiteral(location), copyOpts)
	if err := w.engine.ExecWrite(copySQL, bindings, accessKey, secretKey, endpoint); err != nil {
		return nil, fmt.Errorf("ctas copy: %w", err)
	}

	// Probe reader for schema/stats inference over the written files.
	probe := ctasProbeReader(location, len(plan.PartitionBy) > 0)
	tbl, err := w.cat.RegisterManaged(ctx, catalog.RegisterManagedInput{
		ProjectID:        projectID,
		Dataset:          plan.Dataset,
		Name:             plan.Table,
		Location:         location,
		ProbeReader:      probe,
		StorageClass:     storageClass,
		PartitionColumns: plan.PartitionBy,
	}, accessKey, secretKey, endpoint)
	if err != nil {
		return nil, err
	}
	if err := w.afterWrite(ctx, projectID, plan.Dataset, plan.Table); err != nil {
		return nil, err
	}
	return tbl, nil
}

// ctasProbeReader returns a read_parquet(...) expression covering the written
// files. Partitioned output lives in nested dt=.../ dirs and needs the glob +
// hive_partitioning flag so the partition columns appear in the schema.
func ctasProbeReader(location string, partitioned bool) string {
	if partitioned {
		return fmt.Sprintf("read_parquet('%s/**/*.parquet', hive_partitioning=true)", escapeLiteral(location))
	}
	return fmt.Sprintf("read_parquet('%s/*.parquet')", escapeLiteral(location))
}
