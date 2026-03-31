package configsdk

import (
	"errors"
	"net/http"
	"testing"
)

func TestMapStatusError(t *testing.T) {
	tests := []struct {
		name  string
		code  int
		want  error
		isNil bool
	}{
		{"200 OK", http.StatusOK, nil, true},
		{"201 Created", http.StatusCreated, nil, true},
		{"204 No Content", http.StatusNoContent, nil, true},
		{"401 Unauthorized", http.StatusUnauthorized, ErrUnauthorized, false},
		{"403 Forbidden", http.StatusForbidden, ErrForbidden, false},
		{"404 Not Found", http.StatusNotFound, ErrNotFound, false},
		{"400 Bad Request", http.StatusBadRequest, ErrInvalidResponse, false},
		{"422 Unprocessable", 422, ErrInvalidResponse, false},
		{"500 Internal Server Error", http.StatusInternalServerError, ErrConnectionFailed, false},
		{"502 Bad Gateway", http.StatusBadGateway, ErrConnectionFailed, false},
		{"503 Service Unavailable", http.StatusServiceUnavailable, ErrConnectionFailed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := mapStatusError(tt.code)
			if tt.isNil {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("got nil, want %v", tt.want)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRetriableError(t *testing.T) {
	inner := errors.New("network timeout")
	re := &retriableError{err: inner}

	if re.Error() != "network timeout" {
		t.Errorf("Error() = %q", re.Error())
	}
	if re.Unwrap() != inner {
		t.Error("Unwrap() did not return inner error")
	}
	if !isRetriable(re) {
		t.Error("isRetriable returned false for retriableError")
	}
	if isRetriable(errors.New("plain error")) {
		t.Error("isRetriable returned true for plain error")
	}
}
