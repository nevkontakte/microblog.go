package judge

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Query runs a read-only query against an instance's database while it is
// running. The database format is part of the compatibility contract
// (ADR 0003), so scenarios may assert on it directly.
func Query(i Instance, query string, args ...any) ([]map[string]any, error) {
	path := filepath.Join(i.DataDir(), "microblogpub.db")
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", query, err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Exec writes to an instance's database while it runs. Scenarios use it only
// to seed state the original tests seeded directly and that no interaction
// can produce, such as a counter that has drifted.
func Exec(i Instance, query string, args ...any) error {
	path := filepath.Join(i.DataDir(), "microblogpub.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(query, args...)
	return err
}
