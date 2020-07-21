package cmgr

import (
	"fmt"
	"strconv"
)

func isEmptyQueryError(err error) bool {
	return err.Error() == "sql: no rows in result set"
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
