package cmgr

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

type InvalidInputError struct {
	Err error
}

func (e *InvalidInputError) Error() string {
	return e.Err.Error()
}

func (e *InvalidInputError) Unwrap() error {
	return e.Err
}

func invalidInput(err error) error {
	if err == nil {
		return nil
	}
	var invalid *InvalidInputError
	if errors.As(err, &invalid) {
		return err
	}
	return &InvalidInputError{Err: err}
}

type ConflictError struct {
	Err error
}

func (e *ConflictError) Error() string {
	return e.Err.Error()
}

func (e *ConflictError) Unwrap() error {
	return e.Err
}

func isEmptyQueryError(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func unknownChallengeIdError(id ChallengeId) error {
	return &UnknownIdentifierError{Type: "challenge", Name: string(id)}
}

func unknownBuildIdError(id BuildId) error {
	return &UnknownIdentifierError{Type: "build", Name: strconv.FormatInt(int64(id), 10)}
}

func unknownInstanceIdError(id InstanceId) error {
	return &UnknownIdentifierError{Type: "instance", Name: strconv.FormatInt(int64(id), 10)}
}

func unknownSchemaIdError(id string) error {
	return &UnknownIdentifierError{Type: "schema", Name: id}
}

func (e *UnknownIdentifierError) Error() string {
	return fmt.Sprintf("unknown %s identifier: %s", e.Type, e.Name)
}
