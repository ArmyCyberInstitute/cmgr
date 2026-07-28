package cmgr

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// SeccompTweakList stores build-discovered runtime requirements as JSON in
// SQLite while retaining an ordinary JSON array in the public API.
type SeccompTweakList []string

func (tweaks SeccompTweakList) Value() (driver.Value, error) {
	if tweaks == nil {
		return "[]", nil
	}
	data, err := json.Marshal(tweaks)
	if err != nil {
		return nil, fmt.Errorf("could not encode required seccomp tweaks: %v", err)
	}
	return string(data), nil
}

func (tweaks *SeccompTweakList) Scan(value interface{}) error {
	if value == nil {
		*tweaks = nil
		return nil
	}

	var data []byte
	switch value := value.(type) {
	case string:
		data = []byte(value)
	case []byte:
		data = value
	default:
		return fmt.Errorf("could not decode required seccomp tweaks from %T", value)
	}

	var decoded []string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("could not decode required seccomp tweaks: %v", err)
	}
	*tweaks = decoded
	return nil
}
