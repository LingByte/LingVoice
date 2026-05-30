package compose

import "encoding/json"

// MarshalCheckpointForTest serializes a checkpoint for tests.
func MarshalCheckpointForTest(cp *Checkpoint) ([]byte, error) {
	if cp == nil {
		return nil, nil
	}
	return json.Marshal(cp)
}
