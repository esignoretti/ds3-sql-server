package write

import (
	"fmt"
	"regexp"
	"strings"
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
