package shared

import "reflect"

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
