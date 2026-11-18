package sqlite

import (
	"database/sql/driver"
	"errors"
	"strings"
	"unicode"

	sqlitedriver "modernc.org/sqlite"
)

func init() {
	sqlitedriver.MustRegisterDeterministicScalarFunction("steward_task_fold", 1, func(_ *sqlitedriver.FunctionContext, args []driver.Value) (driver.Value, error) {
		value, ok := args[0].(string)
		if !ok {
			return nil, errors.New("task search requires text")
		}
		return strings.Map(func(r rune) rune {
			// The minimum code point is stable across the entire simple-fold cycle,
			// including the medial and final forms of Greek sigma.
			first := r
			for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
				first = min(first, next)
			}
			return first
		}, value), nil
	})
}
