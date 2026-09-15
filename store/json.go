package store

import "encoding/json"

// jsonUnmarshal keeps the encoding/json dependency in one place, so the
// store's payload handling is easy to change if the wire format evolves.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
