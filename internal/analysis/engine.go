package analysis

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

type ColumnInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type AnalysisRequest struct {
	Columns []ColumnInfo `json:"columns"`
	Rows    [][]any      `json:"rows"`
}

type ColumnAnalysis struct {
	Type      string        `json:"type"`
	Stats     any           `json:"stats"`
	Histogram []Bin         `json:"histogram,omitempty"`
	TopValues []ValueCount  `json:"top_values,omitempty"`
}

type Bin struct {
	BinStart float64 `json:"bin_start"`
	BinEnd   float64 `json:"bin_end"`
	Count    int     `json:"count"`
}

type ValueCount struct {
	Value string  `json:"value"`
	Count int     `json:"count"`
	Pct   float64 `json:"pct"`
}

type Correlation struct {
	ColA  string  `json:"col_a"`
	ColB  string  `json:"col_b"`
	Type  string  `json:"type"`
	Value float64 `json:"value"`
}

type AnalysisResult struct {
	Columns      map[string]ColumnAnalysis `json:"columns"`
	Correlations []Correlation             `json:"correlations"`
	Summary      []string                  `json:"summary"`
	ElapsedMs    int64                     `json:"elapsed_ms"`
	Error        string                    `json:"error,omitempty"`
}

type Engine struct {
	pool chan *sql.DB
}

func NewEngine(pool chan *sql.DB) *Engine {
	return &Engine{pool: pool}
}

func (e *Engine) Analyze(req AnalysisRequest) *AnalysisResult {
	start := time.Now()

	if len(req.Columns) == 0 {
		return &AnalysisResult{Error: "no columns provided", ElapsedMs: time.Since(start).Milliseconds()}
	}
	if len(req.Rows) == 0 {
		return &AnalysisResult{Error: "no rows provided", ElapsedMs: time.Since(start).Milliseconds()}
	}

	db := <-e.pool
	defer func() { e.pool <- db }()

	colAliases := make([]string, len(req.Columns))
	for i := range req.Columns {
		colAliases[i] = fmt.Sprintf("c%d", i)
	}

	var rowVals []string
	for ri, row := range req.Rows {
		vals := make([]string, len(row))
		for vi, v := range row {
			if v == nil {
				vals[vi] = "NULL"
			} else {
				switch val := v.(type) {
				case string:
					vals[vi] = "'" + strings.ReplaceAll(val, "'", "''") + "'"
				case bool:
					if val {
						vals[vi] = "TRUE"
					} else {
						vals[vi] = "FALSE"
					}
				case float64:
					vals[vi] = fmt.Sprintf("%v", val)
				case float32:
					vals[vi] = fmt.Sprintf("%v", val)
				case int:
					vals[vi] = fmt.Sprintf("%d", val)
				case int64:
					vals[vi] = fmt.Sprintf("%d", val)
				case int32:
					vals[vi] = fmt.Sprintf("%d", val)
				default:
					return &AnalysisResult{Error: fmt.Sprintf("unsupported value type %T at row %d col %s", v, ri, req.Columns[vi].Name), ElapsedMs: time.Since(start).Milliseconds()}
				}
			}
		}
		rowVals = append(rowVals, "("+strings.Join(vals, ",")+")")
	}

	createSQL := fmt.Sprintf("CREATE TEMP TABLE analysis_data AS SELECT * FROM (VALUES %s) AS t(%s)",
		strings.Join(rowVals, ","), strings.Join(colAliases, ","))

	if _, err := db.Exec(createSQL); err != nil {
		return &AnalysisResult{Error: "create temp table: " + err.Error(), ElapsedMs: time.Since(start).Milliseconds()}
	}
	defer db.Exec("DROP TABLE IF EXISTS analysis_data")

	columns := make(map[string]ColumnAnalysis)
	var summary []string
	var correlations []Correlation

	for _, c := range req.Columns {
		colIdx := colIndex(req.Columns, c.Name)
		if colIdx < 0 {
			continue
		}
		alias := fmt.Sprintf("c%d", colIdx)
		duckType := mapType(c.Type)

		switch {
		case strings.Contains(duckType, "INT") || strings.Contains(duckType, "FLOAT") || strings.Contains(duckType, "DOUBLE") || strings.Contains(duckType, "DECIMAL") || strings.Contains(duckType, "NUMERIC"):
			var count, nullCount, distinctCount int
			var min, max, avg, stddev *float64
			statsSQL := fmt.Sprintf(`SELECT COUNT(*), MIN(%s), MAX(%s), AVG(%s), STDDEV_SAMP(%s), COUNT(*) FILTER (WHERE %s IS NULL), COUNT(DISTINCT %s) FROM analysis_data`,
				alias, alias, alias, alias, alias, alias)
			row := db.QueryRow(statsSQL)
			if err := row.Scan(&count, &min, &max, &avg, &stddev, &nullCount, &distinctCount); err != nil {
				summary = append(summary, fmt.Sprintf("%s: could not compute stats (%s)", c.Name, err.Error()))
				continue
			}

			stats := map[string]any{
				"count":       float64(count),
				"null_count":  float64(nullCount),
				"distinct":    float64(distinctCount),
				"min":         safeFloat(min),
				"max":         safeFloat(max),
				"mean":        safeFloat(avg),
				"stddev":      safeFloat(stddev),
			}

			if min != nil && max != nil && *min < *max {
				binWidth := (*max - *min) / 10.0
				histSQL := fmt.Sprintf(`SELECT
					LEAST(CAST(FLOOR((%s - %f) / %f) AS BIGINT), 9) AS bucket,
					MIN(%s), MAX(%s), COUNT(*)
					FROM analysis_data WHERE %s IS NOT NULL
					GROUP BY bucket ORDER BY bucket`,
					alias, *min, binWidth, alias, alias, alias)
				hRows, err := db.Query(histSQL)
				if err == nil {
					var bins []Bin
					for hRows.Next() {
						var bucket int64
						var b Bin
						if err := hRows.Scan(&bucket, &b.BinStart, &b.BinEnd, &b.Count); err == nil {
							bins = append(bins, b)
						}
					}
					hRows.Close()
					if len(bins) > 0 {
						columns[c.Name] = ColumnAnalysis{
							Type:      "numeric",
							Stats:     stats,
							Histogram: bins,
						}
					} else {
						columns[c.Name] = ColumnAnalysis{Type: "numeric", Stats: stats}
					}
				} else {
					columns[c.Name] = ColumnAnalysis{Type: "numeric", Stats: stats}
				}
			} else {
				columns[c.Name] = ColumnAnalysis{Type: "numeric", Stats: stats}
			}

			nullPct := 0.0
			if count > 0 {
				nullPct = float64(nullCount) / float64(count) * 100
			}
			summary = append(summary, fmt.Sprintf("%s ranges from %.2f to %.2f (mean: %.2f, stddev: %.2f). %.1f%% null values.",
				c.Name, safeFloat(min), safeFloat(max), safeFloat(avg), safeFloat(stddev), nullPct))

		case duckType == "VARCHAR":
			var count, nullCount, distinctCount int
			countSQL := fmt.Sprintf("SELECT COUNT(*), COUNT(*) FILTER (WHERE %s IS NULL), COUNT(DISTINCT %s) FROM analysis_data", alias, alias)
			row := db.QueryRow(countSQL)
			if err := row.Scan(&count, &nullCount, &distinctCount); err != nil {
				summary = append(summary, fmt.Sprintf("%s: could not compute stats (%s)", c.Name, err.Error()))
				continue
			}

			stats := map[string]any{
				"count":      count,
				"null_count": nullCount,
				"distinct":   distinctCount,
			}

			topSQL := fmt.Sprintf("SELECT %s, COUNT(*) as cnt FROM analysis_data WHERE %s IS NOT NULL GROUP BY %s ORDER BY cnt DESC LIMIT 10", alias, alias, alias)
			tRows, err := db.Query(topSQL)
			var topValues []ValueCount
			if err == nil {
				for tRows.Next() {
					var val string
					var cnt int
					if err := tRows.Scan(&val, &cnt); err == nil {
						pct := 0.0
						if count > 0 {
							pct = float64(cnt) / float64(count) * 100
						}
						topValues = append(topValues, ValueCount{Value: val, Count: cnt, Pct: pct})
					}
				}
				tRows.Close()
			}

			columns[c.Name] = ColumnAnalysis{
				Type:      "categorical",
				Stats:     stats,
				TopValues: topValues,
			}

			top1 := ""
			top1Pct := 0.0
			if len(topValues) > 0 {
				top1 = topValues[0].Value
				top1Pct = topValues[0].Pct
			}
			summary = append(summary, fmt.Sprintf("%s has %d distinct values; '%s' is most common (%.1f%%).",
				c.Name, distinctCount, top1, top1Pct))

		case duckType == "TIMESTAMP":
			var count, nullCount int
			var minStr, maxStr *string
			countSQL := fmt.Sprintf("SELECT COUNT(*), COUNT(*) FILTER (WHERE %s IS NULL), MIN(%s::VARCHAR), MAX(%s::VARCHAR) FROM analysis_data", alias, alias, alias)
			row := db.QueryRow(countSQL)
			if err := row.Scan(&count, &nullCount, &minStr, &maxStr); err != nil {
				summary = append(summary, fmt.Sprintf("%s: could not compute stats (%s)", c.Name, err.Error()))
				continue
			}

			minVal := ""
			maxVal := ""
			if minStr != nil {
				minVal = *minStr
			}
			if maxStr != nil {
				maxVal = *maxStr
			}

			columns[c.Name] = ColumnAnalysis{
				Type: "temporal",
				Stats: map[string]any{
					"count":      count,
					"null_count": nullCount,
					"min":        minVal,
					"max":        maxVal,
				},
			}
			summary = append(summary, fmt.Sprintf("%s spans from %s to %s.", c.Name, minVal, maxVal))

		case duckType == "BOOLEAN":
			var count, nullCount, trueCount int
			boolSQL := fmt.Sprintf("SELECT COUNT(*), COUNT(*) FILTER (WHERE %s IS NULL), SUM(CASE WHEN %s THEN 1 ELSE 0 END) FROM analysis_data", alias, alias)
			row := db.QueryRow(boolSQL)
			if err := row.Scan(&count, &nullCount, &trueCount); err != nil {
				summary = append(summary, fmt.Sprintf("%s: could not compute stats (%s)", c.Name, err.Error()))
				continue
			}

			truePct := 0.0
			if count > 0 {
				truePct = float64(trueCount) / float64(count) * 100
			}
			columns[c.Name] = ColumnAnalysis{
				Type: "boolean",
				Stats: map[string]any{
					"count":      count,
					"null_count": nullCount,
					"true_count": trueCount,
					"true_pct":   truePct,
				},
			}
			summary = append(summary, fmt.Sprintf("%s is true for %.1f%% of rows.", c.Name, truePct))

		default:
			columns[c.Name] = ColumnAnalysis{Type: "categorical", Stats: map[string]any{"note": "unknown type, treated as categorical"}}
		}
	}

	numCols := make([]ColumnInfo, 0)
	for _, c := range req.Columns {
		dt := mapType(c.Type)
		if strings.Contains(dt, "INT") || strings.Contains(dt, "FLOAT") || strings.Contains(dt, "DOUBLE") || strings.Contains(dt, "DECIMAL") {
			numCols = append(numCols, c)
		}
	}
	for i := 0; i < len(numCols); i++ {
		for j := i + 1; j < len(numCols); j++ {
			ci := fmt.Sprintf("c%d", colIndex(req.Columns, numCols[i].Name))
			cj := fmt.Sprintf("c%d", colIndex(req.Columns, numCols[j].Name))
			corrSQL := fmt.Sprintf("SELECT CORR(%s, %s) FROM analysis_data", ci, cj)
			var corrVal *float64
			if err := db.QueryRow(corrSQL).Scan(&corrVal); err == nil && corrVal != nil {
				correlations = append(correlations, Correlation{
					ColA: numCols[i].Name, ColB: numCols[j].Name,
					Type: "pearson", Value: *corrVal,
				})
			}
		}
	}

	elapsed := time.Since(start).Milliseconds()
	return &AnalysisResult{
		Columns:      columns,
		Correlations: correlations,
		Summary:      summary,
		ElapsedMs:    elapsed,
	}
}

func mapType(duckType string) string {
	up := strings.ToUpper(duckType)
	switch {
	case strings.Contains(up, "INT") || strings.Contains(up, "DECIMAL") || strings.Contains(up, "FLOAT") || strings.Contains(up, "DOUBLE") || strings.Contains(up, "NUMERIC"):
		return duckType
	case strings.Contains(up, "VARCHAR") || strings.Contains(up, "CHAR") || strings.Contains(up, "TEXT"):
		return "VARCHAR"
	case strings.Contains(up, "TIMESTAMP") || strings.Contains(up, "DATE") || strings.Contains(up, "TIME"):
		return "TIMESTAMP"
	case up == "BOOLEAN" || up == "BOOL":
		return "BOOLEAN"
	default:
		return "VARCHAR"
	}
}

func colIndex(cols []ColumnInfo, name string) int {
	for i, c := range cols {
		if c.Name == name {
			return i
		}
	}
	return -1
}

func safeFloat(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
