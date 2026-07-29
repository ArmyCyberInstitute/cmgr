package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ArmyCyberInstitute/cmgr/cmgr"
)

func TestDecodeJSONEnforcesSizeUnknownFieldsAndSingleValue(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		limit  int64
		target any
	}{
		{
			name:   "too large",
			body:   `{"seeds":[1,2,3,4]}`,
			limit:  8,
			target: new(BuildChallengeRequest),
		},
		{
			name:   "unknown field",
			body:   `{"seedz":[1]}`,
			limit:  1024,
			target: new(BuildChallengeRequest),
		},
		{
			name:   "multiple values",
			body:   `{"seeds":[1]} {"seeds":[2]}`,
			limit:  1024,
			target: new(BuildChallengeRequest),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/",
				strings.NewReader(test.body),
			)
			response := httptest.NewRecorder()
			serverState := state{maxRequestBytes: test.limit}
			if err := serverState.decodeJSON(response, request, test.target); err == nil {
				t.Fatal("invalid JSON request was accepted")
			}
		})
	}
}

func TestBuildHandlerReturnsAfterInvalidIdentifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/builds/not-a-number", nil)
	response := httptest.NewRecorder()
	state{}.buildHandler(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d", response.Code)
	}
}

func TestErrorStatusPreservesTypedErrors(t *testing.T) {
	if status := errorStatus(
		&cmgr.InvalidInputError{Err: errors.New("bad")},
		http.StatusInternalServerError,
	); status != http.StatusBadRequest {
		t.Fatalf("invalid input mapped to %d", status)
	}
	if status := errorStatus(
		&cmgr.ConflictError{Err: errors.New("conflict")},
		http.StatusInternalServerError,
	); status != http.StatusConflict {
		t.Fatalf("conflict mapped to %d", status)
	}
}
