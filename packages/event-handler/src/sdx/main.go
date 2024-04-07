package main

import (
	"context"
	"fmt"
	"os"
	"reflect"

	"cloud.google.com/go/pubsub"
)

func main() {
	//pubsubClient()
	lookupNested()
}

func pubsubClient() {
	ctx := context.Background()
	fmt.Println(os.LookupEnv("GOOGLE_APPLICATION_CREDENTIALS"))
	var projectId string
	if id := os.Getenv("PROJECT_ID"); id == "" {
		projectId = pubsub.DetectProjectID
	} else {
		projectId = id
	}
	client, err := pubsub.NewClient(ctx, projectId)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.Close()
	fmt.Println(client.Project())

}

func lookupNested() {
	m := map[string]interface{}{
		"a": map[string]interface{}{
			"b": map[string]interface{}{
				"c": "d",
			},
		},
	}
	d, ok := m["a"].(map[string]interface{})["b"].(map[string]interface{})["c"].(string)
	fmt.Println(d, ok)

	//missed, ok := m["a"].(map[string]interface{})["missed"].(map[string]interface{})["c"].(string)

	fmt.Println(lookupMap[string](m, "a", "b", "c"))
	fmt.Println(lookupMap[int](m, "a", "b", "c"))
	fmt.Println(lookupMap[string](m, "a", "missed", "c"))
}

func lookupMap[T any](m map[string]interface{}, keys ...string) (T, bool) {
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
