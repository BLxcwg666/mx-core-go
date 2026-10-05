package nullable

import (
	"encoding/json"
	"testing"
)

func TestValueDistinguishesAbsentAndNull(t *testing.T) {
	type body struct {
		A Value[string] `json:"a"`
		B Value[string] `json:"b"`
		C Value[string] `json:"c"`
	}
	var got body
	if err := json.Unmarshal([]byte(`{"a":null,"b":"x"}`), &got); err != nil {
		t.Fatal(err)
	}
	if !got.A.Set || got.A.Valid {
		t.Fatalf("a: want set null, got %+v", got.A)
	}
	if !got.B.Set || !got.B.Valid || got.B.V != "x" {
		t.Fatalf("b: want set x, got %+v", got.B)
	}
	if got.C.Set {
		t.Fatalf("c: want absent, got %+v", got.C)
	}
}
