package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"cloud.google.com/go/pubsub"
	"github.com/sisisin-sandbox/fourkeys-go/shared"
)

type environmentVariables struct {
	projectID           string
	githubWebhookSecret string
	port                string
}

var envVars environmentVariables

func init() {
	envVars.projectID = os.Getenv("PROJECT_ID")
	{
		port, ok := os.LookupEnv("PORT")
		if ok {
			envVars.port = port
		} else {
			envVars.port = "8000"
		}
	}
	envVars.githubWebhookSecret = mustGetenv("GITHUB_WEBHOOK_SECRET")
}

func mustGetenv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		panic(k + " environment variable not set")
	}
	return v
}
func newContext() context.Context {
	ctx := context.Background()
	ctx = shared.WithLogger(ctx)
	return ctx
}
func withLogger(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := shared.WithLogger(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}
func withRequestLog(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := shared.LoggerFromContext(r.Context())

		logger.Info("request received",
			slog.String("method", r.Method),
			slog.String("url", r.URL.String()),
			slog.Any("header", r.Header),
		)
		next.ServeHTTP(w, r)
	}
}

func main() {
	ctx := newContext()
	logger := shared.LoggerFromContext(ctx)
	http.HandleFunc("/", withLogger(withRequestLog(index)))

	addr := ":" + envVars.port
	logger.Info(fmt.Sprintf("listening on %s", addr))
	http.ListenAndServe(addr, nil)
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
		name: "github",
		//signature:    "X-Hub-Signature",
		signature:    "X-Hub-Signature-256",
		verification: verifyGithubSignature256,
	},
}

func verifyGithubSignature256(signature string, body []byte) bool {
	h := hmac.New(sha256.New, []byte(envVars.githubWebhookSecret))
	h.Write(body)
	expectedMAC := h.Sum(nil)

	signaturePrefix := "sha256="
	signatureMAC, err := hex.DecodeString(signature[len(signaturePrefix):])
	if err != nil {
		return false
	}

	return hmac.Equal(signatureMAC, expectedMAC)
}

func verifyGithubSignature(signature string, body []byte) bool {
	h := hmac.New(sha1.New, []byte(envVars.githubWebhookSecret))
	h.Write(body)
	expectedMAC := h.Sum(nil)
	expectedStr := hex.EncodeToString(expectedMAC)
	fmt.Println("expectedStr", expectedStr)

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

	b, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !authSource.verification(signature, b) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	pubsubHeaders := make(map[string][]string)
	for k, v := range r.Header {
		if k == "Authorization" {
			continue
		}
		pubsubHeaders[k] = v
	}

	logger := shared.LoggerFromContext(r.Context())
	err = publishToPubsub(r.Context(), authSource, pubsubHeaders, b)
	if err != nil {
		logger.Error("error publishing to pubsub", slog.Any("error", err))
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func publishToPubsub(ctx context.Context, source eventSource, header map[string][]string, body []byte) error {
	logger := shared.LoggerFromContext(ctx)
	projectID := envVars.projectID
	if projectID == "" {
		projectID = pubsub.DetectProjectID
	}

	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return err
	}
	defer client.Close()

	{
		var bodyMap any
		err = json.Unmarshal(body, &bodyMap)
		if err != nil {
			logger.Error("error unmarshalling request", slog.Any("error", err))
			return err
		}

		logger.Info("publishing to pubsub",
			slog.Any("header", header),
			slog.Any("body", bodyMap),
		)
	}

	headersAttr, err := json.Marshal(header)
	if err != nil {
		logger.Error("error marshalling headers", slog.Any("error", err))
		return err
	}

	res, err := client.Topic(source.name).Publish(ctx, &pubsub.Message{
		Data:       body,
		Attributes: map[string]string{"headers": string(headersAttr)},
	}).Get(ctx)
	if err != nil {
		logger.Error("error publishing to pubsub", slog.Any("error", err))
		return err
	}
	logger.Info("published to pubsub", slog.String("messageID", res))

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
