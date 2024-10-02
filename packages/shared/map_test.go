package shared

import (
	"encoding/json"
	"testing"
)

func TestLookupMapE(t *testing.T) {
	m := map[string]interface{}{
		"key1": map[string]interface{}{
			"key2": "value",
			"key4": map[string]interface{}{
				"key5": 5,
			},
		},
		"key3": "value3",
		"int":  1985130141,
	}
	t.Run("test1", func(t *testing.T) {
		result, err := LookupMapE[string](m, "key1", "key2")
		if err != nil {
			t.Errorf("error: %v", err)
		}
		if result != "value" {
			t.Errorf("result: %v", result)
		}
	})

	t.Run("failure cast map", func(t *testing.T) {
		result, err := LookupMapE[string](m, "key3", "missing")
		if err == nil && err.Error() != "key missing not found in key3" {
			t.Errorf("error: %v", err)
		}
		if result != "" {
			t.Errorf("result: %v", result)
		}
	})

	t.Run("failure key not found", func(t *testing.T) {
		result, err := LookupMapE[string](m, "key1", "key4", "missing")
		if err == nil && err.Error() != "key missing not found in key1.key4" {
			t.Errorf("error: %v", err)
		}
		if result != "" {
			t.Errorf("result: %v", result)
		}
	})

	t.Run("failure cast value", func(t *testing.T) {
		result, err := LookupMapE[int](m, "key1", "key2")
		if err == nil && err.Error() != "value value is not of type int" {
			t.Errorf("error: %v", err)
		}
		if result != 0 {
			t.Errorf("result: %v", result)
		}
	})

	t.Run("failure cast", func(t *testing.T) {
		jsonStr := `{"int": 1985130141}`
		m := map[string]interface{}{}
		err := json.Unmarshal([]byte(jsonStr), &m)
		if err != nil {
			t.Errorf("error: %v", err)
		}

		t.Log(m["int"])
		result, err := LookupMapE[int](m, "int")
		if err != nil {
			t.Errorf("error: %v", err)
		}
		if result != 1985130141 {
			t.Errorf("result: %v", result)
		}
	})
}
