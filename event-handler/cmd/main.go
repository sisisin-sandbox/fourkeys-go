package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"os"
	"strings"

	"cloud.google.com/go/pubsub"
)

type environmentVariables struct {
	projectID           string
	githubWebhookSecret string
}

var envVars environmentVariables

func init() {
	envVars.projectID = os.Getenv("PROJECT_ID")
	envVars.githubWebhookSecret = mustGetenv("GITHUB_WEBHOOK_SECRET")
}

func mustGetenv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		panic(k + " environment variable not set")
	}
	return v
}

func main() {
	http.HandleFunc("/", index)

	http.ListenAndServe(":8000", nil)
}

func index(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		indexPost(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type eventSource struct {
	name         string
	signature    string
	verification func(signature string, body []byte) bool
}

var authorizedSources map[string]eventSource = map[string]eventSource{
	"github": {
		name:         "github",
		signature:    "X-Hub-Signature",
		verification: verifyGithubSignature,
	},
}

// todo: change algorithm to sha256
func verifyGithubSignature(signature string, body []byte) bool {
	h := hmac.New(sha1.New, []byte(envVars.githubWebhookSecret))
	h.Write(body)
	expectedMAC := h.Sum(nil)

	receivedMAC := make([]byte, 20)
	hex.Decode(receivedMAC, []byte(signature[5:]))
	return hmac.Equal(receivedMAC, expectedMAC)
}

func indexPost(w http.ResponseWriter, r *http.Request) {
	source := getSource(r.Header)
	if _, ok := authorizedSources[source]; !ok {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	authSource := authorizedSources[source]

	var signature string
	if v := r.URL.Query().Get(authSource.signature); v != "" {
		signature = v
	} else {
		if v := r.Header.Get(authSource.signature); v != "" {
			signature = v
		} else {
			w.WriteHeader(http.StatusForbidden)
		}
	}

	body := make([]byte, r.ContentLength)
	r.Body.Read(body)
	defer r.Body.Close()
	if !authSource.verification(signature, body) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	pubsubHeaders := make(map[string]any)
	for k, v := range r.Header {
		if k == "Authorization" {
			continue
		}
		pubsubHeaders[k] = v
	}

	publishToPubsub(r.Context(), authSource, pubsubHeaders, body)

	w.WriteHeader(http.StatusNoContent)
}

func publishToPubsub(ctx context.Context, source eventSource, header map[string]any, body any) error {
	projectID := envVars.projectID
	if projectID == "" {
		projectID = pubsub.DetectProjectID
	}

	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return err
	}
	defer client.Close()

	return nil
}

func getSource(header http.Header) string {
	if _, ok := header["X-Gitlab-Event"]; ok {
		return "gitlab"
	}
	if _, ok := header["Ce-Type"]; ok {
		if v := header.Get("Ce-Type"); strings.Contains(v, "tekton") {
			return "tekton"
		}
	}
	if _, ok := header["User-Agent"]; ok {
		if v := header.Get("User-Agent"); strings.Contains(v, "GitHub-Hookshot") {
			return "github"
		}
	}
	if _, ok := header["Circleci-Event-Type"]; ok {
		return "circleci"
	}
	if _, ok := header["X-Pagerduty-Signature"]; ok {
		return "pagerduty"
	}

	return header.Get("User-Agent")
}
