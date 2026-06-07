// Package planner implements catalog-side query planning. In Phase 4 it provides
// partition pruning: mapping simple WHERE predicates over a table's partition
// columns to the reduced set of partitions (and thus reader locations) that can
// possibly match. Pruning is correctness-preserving — it only ever removes
// partitions that cannot match; any predicate it cannot confidently parse is
// ignored, falling back to scanning all partitions.
package planner

import (
	"regexp"
	"strings"
)

// Partition mirrors metastore.Partition but is duplicated here to keep the
// planner package free of a metastore import (avoiding an import cycle: catalog
// imports both). The catalog layer converts between the two.
type Partition struct {
	Values   map[string]string
	Location string
	RowCount int64
	Min      map[string]string
	Max      map[string]string
}

// Op is a comparison operator in a parsed predicate.
type Op int

const (
	OpEq Op = iota
	OpIn
	OpGt
	OpGte
	OpLt
	OpLte
)

// Predicate is a single parsed predicate over a partition column.
type Predicate struct {
	Column string
	Op     Op
	Values []string // for OpIn this holds the whole list; otherwise one element
}

var (
	// `col <op> 'value'` or `col <op> value` (value: quoted string or bareword/number).
	cmpRe = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s*(>=|<=|=|>|<)\s*('(?:[^']|'')*'|[a-z0-9_.\-:]+)`)
	// `col IN ( ... )`
	inRe = regexp.MustCompile(`(?i)\b([a-z_][a-z0-9_]*)\s+IN\s*\(([^)]*)\)`)
	// quoted item extractor for IN lists / general literals
	quotedRe = regexp.MustCompile(`'((?:[^']|'')*)'`)
)

// whereClause returns the text after the first top-level WHERE, or "" if absent.
func whereClause(sql string) string {
	loc := regexp.MustCompile(`(?i)\bWHERE\b`).FindStringIndex(sql)
	if loc == nil {
		return ""
	}
	clause := sql[loc[1]:]
	// Trim trailing clauses we don't analyze; conservative: keep everything up to
	// the first GROUP/ORDER/LIMIT/HAVING/window/semicolon at top level.
	tail := regexp.MustCompile(`(?i)\b(GROUP\s+BY|ORDER\s+BY|LIMIT|HAVING|WINDOW|QUALIFY)\b`).FindStringIndex(clause)
	if tail != nil {
		clause = clause[:tail[0]]
	}
	if i := strings.Index(clause, ";"); i >= 0 {
		clause = clause[:i]
	}
	return clause
}

func unquote(lit string) string {
	if len(lit) >= 2 && lit[0] == '\'' && lit[len(lit)-1] == '\'' {
		return strings.ReplaceAll(lit[1:len(lit)-1], "''", "'")
	}
	return lit
}

func isPartCol(col string, partCols []string) (string, bool) {
	for _, pc := range partCols {
		if strings.EqualFold(pc, col) {
			return pc, true
		}
	}
	return "", false
}

// ParseWhere extracts supported conjunctive predicates over the given partition
// columns. It returns nil when the WHERE clause contains an unsupported top-level
// construct (OR / NOT), signalling callers to scan all partitions.
func ParseWhere(sql string, partCols []string) []Predicate {
	clause := whereClause(sql)
	if clause == "" {
		return nil
	}
	// Unsupported boolean structure -> conservative full scan.
	if regexp.MustCompile(`(?i)\b(OR|NOT)\b`).MatchString(clause) {
		return nil
	}

	var preds []Predicate

	// IN predicates first (so their `=`-free shape isn't mis-scanned).
	inMatches := inRe.FindAllStringSubmatch(clause, -1)
	for _, m := range inMatches {
		col, ok := isPartCol(m[1], partCols)
		if !ok {
			continue
		}
		var vals []string
		for _, q := range quotedRe.FindAllStringSubmatch(m[2], -1) {
			vals = append(vals, strings.ReplaceAll(q[1], "''", "'"))
		}
		if len(vals) == 0 {
			// bareword list, split on commas
			for _, raw := range strings.Split(m[2], ",") {
				if v := strings.TrimSpace(raw); v != "" {
					vals = append(vals, v)
				}
			}
		}
		if len(vals) > 0 {
			preds = append(preds, Predicate{Column: col, Op: OpIn, Values: vals})
		}
	}
	// Blank out IN regions so cmpRe doesn't double-match inside them.
	clauseForCmp := inRe.ReplaceAllString(clause, " ")

	for _, m := range cmpRe.FindAllStringSubmatch(clauseForCmp, -1) {
		col, ok := isPartCol(m[1], partCols)
		if !ok {
			continue
		}
		var op Op
		switch m[2] {
		case "=":
			op = OpEq
		case ">":
			op = OpGt
		case ">=":
			op = OpGte
		case "<":
			op = OpLt
		case "<=":
			op = OpLte
		}
		preds = append(preds, Predicate{Column: col, Op: op, Values: []string{unquote(m[3])}})
	}
	return preds
}

func matches(p Predicate, partVal string) bool {
	switch p.Op {
	case OpEq:
		return partVal == p.Values[0]
	case OpIn:
		for _, v := range p.Values {
			if partVal == v {
				return true
			}
		}
		return false
	case OpGt:
		return partVal > p.Values[0]
	case OpGte:
		return partVal >= p.Values[0]
	case OpLt:
		return partVal < p.Values[0]
	case OpLte:
		return partVal <= p.Values[0]
	}
	return true
}

// Prune returns the subset of partitions that can possibly satisfy the SQL's
// WHERE predicates over the partition columns. Partitions whose value for a
// constrained column is missing are kept (conservative).
func Prune(sql string, partCols []string, partitions []Partition) []Partition {
	preds := ParseWhere(sql, partCols)
	if len(preds) == 0 {
		return partitions
	}
	var out []Partition
	for _, part := range partitions {
		keep := true
		for _, p := range preds {
			val, ok := part.Values[p.Column]
			if !ok {
				continue // unknown value for this partition -> can't exclude
			}
			if !matches(p, val) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, part)
		}
	}
	return out
}

// ReaderLocations builds a DuckDB reader expression over the given partitions'
// locations for the format, e.g. read_parquet(['s3://…/p1', 's3://…/p2']).
func ReaderLocations(partitions []Partition, format string) string {
	quoted := make([]string, len(partitions))
	for i, p := range partitions {
		quoted[i] = "'" + strings.ReplaceAll(p.Location, "'", "''") + "'"
	}
	list := "[" + strings.Join(quoted, ", ") + "]"
	switch strings.ToLower(format) {
	case "json":
		return "read_json_auto(" + list + ")"
	case "tsv":
		return "read_csv_auto(" + list + ", delim='\t')"
	case "csv":
		return "read_csv_auto(" + list + ")"
	default: // parquet
		return "read_parquet(" + list + ")"
	}
}
