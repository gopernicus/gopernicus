package conversion

import (
	"encoding/json"
	"testing"
)

func TestJSONOrEmptyObject(t *testing.T) {
	valid := json.RawMessage(`{"key":"value"}`)
	if got := JSONOrEmptyObject(&valid); string(got) != `{"key":"value"}` {
		t.Errorf("JSONOrEmptyObject(valid) = %s, want {\"key\":\"value\"}", got)
	}

	if got := JSONOrEmptyObject(nil); string(got) != "{}" {
		t.Errorf("JSONOrEmptyObject(nil) = %s, want {}", got)
	}

	null := json.RawMessage("null")
	if got := JSONOrEmptyObject(&null); string(got) != "{}" {
		t.Errorf("JSONOrEmptyObject(null) = %s, want {}", got)
	}

	empty := json.RawMessage("")
	if got := JSONOrEmptyObject(&empty); string(got) != "{}" {
		t.Errorf("JSONOrEmptyObject(empty) = %s, want {}", got)
	}
}

func TestJSONOrEmpty(t *testing.T) {
	valid := json.RawMessage(`[1,2,3]`)
	if got := JSONOrEmpty(&valid); string(got) != "[1,2,3]" {
		t.Errorf("JSONOrEmpty(valid) = %s, want [1,2,3]", got)
	}

	if got := JSONOrEmpty(nil); string(got) != "{}" {
		t.Errorf("JSONOrEmpty(nil) = %s, want {}", got)
	}
}
