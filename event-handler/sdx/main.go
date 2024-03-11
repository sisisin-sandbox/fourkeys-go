package main

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/pubsub"
)

func main() {
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
