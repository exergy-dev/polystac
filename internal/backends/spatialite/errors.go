//go:build cgo && spatialite

package spatialite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mattn/go-sqlite3"

	"github.com/example/polystac/pkg/repository"
)

// mapSQLiteErr translates driver / database errors into the sentinel
// errors callers above repository.Repository expect. Returns nil on nil
// input.
func mapSQLiteErr(err error, ctxStr string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", ctxStr, repository.ErrNotFound)
	}
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code {
		case sqlite3.ErrConstraint:
			// PK / UNIQUE / FK violations.
			return fmt.Errorf("%s: %w", ctxStr, repository.ErrConflict)
		case sqlite3.ErrBusy, sqlite3.ErrLocked:
			return fmt.Errorf("%s: %w", ctxStr, repository.ErrBackendUnavailable)
		case sqlite3.ErrMisuse, sqlite3.ErrError:
			// Fall through to the message-based heuristics — this
			// covers, e.g., "no such column" surfaced by ErrError.
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "constraint failed"),
		strings.Contains(msg, "unique constraint"):
		return fmt.Errorf("%s: %w", ctxStr, repository.ErrConflict)
	case strings.Contains(msg, "no such") || strings.Contains(msg, "not found"):
		return fmt.Errorf("%s: %w", ctxStr, repository.ErrNotFound)
	case strings.Contains(msg, "syntax") || strings.Contains(msg, "malformed"):
		return fmt.Errorf("%s: %w", ctxStr, repository.ErrInvalidInput)
	}
	return fmt.Errorf("%s: %w", ctxStr, err)
}
