package conversion

import "encoding/json"

// JSONOrEmptyObject returns the JSON as a RawMessage, or an empty JSON object
// if nil, empty, or null. Guarantees a valid JSON object is always returned.
func JSONOrEmptyObject(j *json.RawMessage) json.RawMessage {
	if j == nil || len(*j) == 0 || string(*j) == "null" {
		return json.RawMessage("{}")
	}
	return *j
}

// JSONOrEmpty returns the JSON as bytes, or empty object bytes if nil.
func JSONOrEmpty(j *json.RawMessage) []byte {
	if j == nil {
		return []byte("{}")
	}
	return []byte(*j)
}
