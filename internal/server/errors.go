// Package server implements PolyStac's HTTP routing and service layer.
// It is backend-agnostic: handlers talk only to the Repository interface
// and the optional sub-interfaces (Aggregator, Queryables).
package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/example/polystac/pkg/cql2"
	"github.com/example/polystac/pkg/repository"
)

// errorBody is the JSON envelope returned for non-2xx responses. It
// matches the shape `stac-fastapi` returns: a `code` and `description`
// pair so existing clients keep working unchanged.
type errorBody struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

func writeError(w http.ResponseWriter, log *slog.Logger, err error) {
	status, code := classify(err)
	if log != nil && status >= 500 {
		log.Error("server error", "err", err.Error())
	}
	body, _ := json.Marshal(errorBody{Code: code, Description: err.Error()})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// classify maps a Repository / cql2 error to an HTTP status and a STAC-API
// flavored error code. Unknown errors fall through to 500.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return http.StatusNotFound, "NotFoundError"
	case errors.Is(err, repository.ErrConflict):
		return http.StatusConflict, "ConflictError"
	case errors.Is(err, repository.ErrInvalidInput):
		return http.StatusBadRequest, "InvalidParameterValue"
	case errors.Is(err, repository.ErrNotImplemented):
		return http.StatusNotImplemented, "NotImplementedError"
	case errors.Is(err, repository.ErrBackendUnavailable):
		return http.StatusServiceUnavailable, "BackendUnavailable"
	case cql2.IsParseError(err), cql2.IsTranslationError(err):
		return http.StatusBadRequest, "InvalidQuery"
	}
	return http.StatusInternalServerError, "InternalError"
}
