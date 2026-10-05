package nullable

import (
	"bytes"
	"encoding/json"
)

// Value tells an absent JSON field apart from an explicit null, so PATCH bodies can clear a field.
type Value[T any] struct {
	Set   bool // the key was present in the body
	Valid bool // the value was not null
	V     T
}

func (v *Value[T]) UnmarshalJSON(data []byte) error {
	v.Set = true
	var zero T
	v.V = zero
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		v.Valid = false
		return nil
	}
	if err := json.Unmarshal(data, &v.V); err != nil {
		return err
	}
	v.Valid = true
	return nil
}

func (v Value[T]) MarshalJSON() ([]byte, error) {
	if !v.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(v.V)
}

// Ptr returns nil for an explicit null, otherwise a pointer to the value.
func (v Value[T]) Ptr() *T {
	if !v.Valid {
		return nil
	}
	val := v.V
	return &val
}
