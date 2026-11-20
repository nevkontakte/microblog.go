package judge

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Query runs a read-only query against an instance's database while it is
// running. The database format is part of the compatibility contract
// (ADR 0003), so scenarios may assert on it directly.
func Query(i Instance, query string, args ...any) (_ []map[string]any, err error) {
	path := filepath.Join(i.DataDir(), "microblogpub.db")
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", query, err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
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
