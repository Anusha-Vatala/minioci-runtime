// json_helpers.go - internal JSON helpers used by both production code and tests.
package runtime

import "encoding/json"

// encodeJSON marshals v to indented JSON.
func encodeJSON(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}

// decodeJSON unmarshals JSON data into v.
func decodeJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
