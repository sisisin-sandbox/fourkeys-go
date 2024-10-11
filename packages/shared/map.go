package shared

import (
	"fmt"
	"reflect"
	"strings"
)

func LookupMap[T any](m map[string]interface{}, keys ...string) (T, bool) {
	var ok bool
	var val interface{} = m

	for _, key := range keys {
		m, ok = val.(map[string]interface{})
		if !ok {
			return reflect.Zero(reflect.TypeOf((*T)(nil)).Elem()).Interface().(T), false
		}

		val, ok = m[key]
		if !ok {
			return reflect.Zero(reflect.TypeOf((*T)(nil)).Elem()).Interface().(T), false
		}
	}

	result, ok := val.(T)
	if !ok {
		return reflect.Zero(reflect.TypeOf((*T)(nil)).Elem()).Interface().(T), false
	}

	return result, true
}

func LookupMapE[T any](m map[string]interface{}, keys ...string) (T, error) {
	var ok bool
	var val interface{} = m
	walkedKeys := make([]string, 0, len(keys))

	zero := reflect.Zero(reflect.TypeOf((*T)(nil)).Elem()).Interface().(T)

	for _, key := range keys {
		m, ok = val.(map[string]interface{})
		if !ok {
			return zero, fmt.Errorf("key %s is not a map: %v", strings.Join(walkedKeys, "."), m)
		}
		walkedKeys = append(walkedKeys, key)

		val, ok = m[key]
		if !ok {
			return zero, fmt.Errorf("key %s not found in %v", strings.Join(walkedKeys, "."), m)
		}
	}

	result, ok := val.(T)
	if !ok {
		return zero, fmt.Errorf("value %v is not of type %T", val, result)
	}

	return result, nil
}
