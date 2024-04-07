package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/sisisin-sandbox/fourkeys-go/shared"
)

type environmentVariables struct {
	port      string
	projectID string
}

var envVars environmentVariables

func init() {
	envVars.projectID = os.Getenv("PROJECT_ID")
	{
		port, ok := os.LookupEnv("PORT")
		if ok {
			envVars.port = port
		} else {
			envVars.port = "8080"
		}
	}
}

func newContext() context.Context {
	ctx := context.Background()
	ctx = shared.WithLogger(ctx)
	return ctx
}

func main() {
	mainContext := newContext()

	http.HandleFunc("/", withLogger(index))

	logger := shared.LoggerFromContext(mainContext)
	addr := ":" + envVars.port
	logger.Info(fmt.Sprintf("listening on %s", addr))
	http.ListenAndServe(addr, nil)
}

func withLogger(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := shared.WithLogger(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func index(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		indexPost(w, r)
	case "GET":
		indexPost(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func indexPost(w http.ResponseWriter, r *http.Request) {
	logger := shared.LoggerFromContext(r.Context())
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error(fmt.Sprintf("error reading request body: %s", err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	logger.Info("received request", slog.String("body", string(body)))

	var msg pubsubRequest
	err = json.Unmarshal(body, &msg)
	if err != nil {
		logger.Error(fmt.Sprintf("error unmarshalling request: %s", err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var attrs map[string][]string
	err = json.Unmarshal([]byte(msg.Message.Attributes.Headers), &attrs)
	if err != nil {
		logger.Error(fmt.Sprintf("error unmarshalling headers: %s", err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	data, err := base64.StdEncoding.DecodeString(msg.Message.Data)
	if err != nil {
		logger.Error(fmt.Sprintf("error decoding data: %s", err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var metadata map[string]interface{}
	err = json.Unmarshal(data, &metadata)
	if err != nil {
		logger.Error(fmt.Sprintf("error unmarshalling data: %s", err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	logger.Info("parsed request",
		slog.Any("attrs", attrs),
		slog.Any("data", metadata),
	)

	event, err := processGithubEvent(r.Context(), msg, attrs, metadata, data)
	if err != nil {
		logger.Error(fmt.Sprintf("error processing github event: %s", err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if event != nil {
		err = insertIntoBigQuery(r.Context(), event)
		if err != nil {
			logger.Error(fmt.Sprintf("error inserting into bigquery: %s", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

type pubsubRequest struct {
	Message struct {
		MessageId   string    `json:"messageId"`
		PublishTime time.Time `json:"publishTime"`
		Data        string    `json:"data"`
		Attributes  struct {
			Headers string `json:"headers"`
		} `json:"attributes"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

type EventRecord struct {
	EventType   string    `bigquery:"event_type"`
	Id          string    `bigquery:"id"`
	Metadata    string    `bigquery:"metadata"`
	TimeCreated time.Time `bigquery:"time_created"`
	Signature   string    `bigquery:"signature"`
	MsgId       string    `bigquery:"msg_id"`
	Source      string    `bigquery:"source"`
}

func insertIntoBigQuery(ctx context.Context, event *EventRecord) error {
	projectID := envVars.projectID
	if projectID == "" {
		projectID = bigquery.DetectProjectID
	}
	client, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		return err
	}
	defer client.Close()

	dataset := client.Dataset("four_keys")
	table := dataset.Table("events_raw")

	inserter := table.Inserter()
	if err := inserter.Put(ctx, event); err != nil {
		return err
	}

	return nil
}

var eventTypes = map[string]bool{
	"push":                        true,
	"pull_request":                true,
	"pull_request_review":         true,
	"pull_request_review_comment": true,
	"issues":                      true,
	"issue_comment":               true,
	"check_run":                   true,
	"check_suite":                 true,
	"status":                      true,
	"deployment_status":           true,
	"release":                     true,
}

func processGithubEvent(
	ctx context.Context,
	reqMessage pubsubRequest,
	headers map[string][]string,
	metadata map[string]interface{},
	rawMetadata []byte,
) (*EventRecord, error) {
	logger := shared.LoggerFromContext(ctx)
	eventType := headers["X-Github-Event"][0]
	if ok := eventTypes[eventType]; !ok {
		logger.Warn(fmt.Sprintf("event type %s is not supported", eventType))
		return nil, nil
	}

	signature := headers["X-Hub-Signature-256"][0]
	var source string
	if _, ok := headers["Mock"]; ok {
		source = "github_mock"
	} else {
		source = "github"
	}

	var (
		maybeTimeCreated, id string
		tOk, iOk             bool
	)
	switch eventType {
	case "push":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "head_commit", "timestamp")
		id, iOk = shared.LookupMap[string](metadata, "head_commit", "id")
	case "pull_request":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "pull_request", "updated_at")
		{
			repoName, rOk := shared.LookupMap[string](metadata, "repository", "name")
			number, nOk := shared.LookupMap[int](metadata, "number")
			if rOk && nOk {
				id = fmt.Sprintf("%s/%d", repoName, number)
				iOk = true
			}
		}
	case "pull_request_review":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "review", "submitted_at")
		id, iOk = shared.LookupMap[string](metadata, "review", "id")
	case "pull_request_review_comment":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "comment", "updated_at")
		id, iOk = shared.LookupMap[string](metadata, "review", "id")
	case "issues":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "issue", "updated_at")
		{
			repoName, rOk := shared.LookupMap[string](metadata, "repository", "name")
			number, nOk := shared.LookupMap[int](metadata, "issue", "number")
			if rOk && nOk {
				id = fmt.Sprintf("%s/%d", repoName, number)
				iOk = true
			}
		}
	case "issue_comment":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "comment", "updated_at")
		id, iOk = shared.LookupMap[string](metadata, "comment", "id")
	case "check_run":
		{
			completedAt, cOk := shared.LookupMap[string](metadata, "check_run", "completed_at")
			if cOk {
				maybeTimeCreated = completedAt
				tOk = true
			} else {
				startedAt, sOk := shared.LookupMap[string](metadata, "check_run", "started_at")
				if sOk {
					maybeTimeCreated = startedAt
					tOk = true
				}
			}
		}
		id, iOk = shared.LookupMap[string](metadata, "check_run", "id")

	case "check_suite":
		{
			updatedAt, uOk := shared.LookupMap[string](metadata, "check_suite", "updated_at")
			if uOk {
				maybeTimeCreated = updatedAt
				tOk = true
			} else {
				createdAd, cOk := shared.LookupMap[string](metadata, "check_suite", "created_at")
				if cOk {
					maybeTimeCreated = createdAd
					tOk = true
				}
			}
		}
		id, iOk = shared.LookupMap[string](metadata, "check_suite", "id")
	case "deployment_status":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "deployment_status", "updated_at")
		id, iOk = shared.LookupMap[string](metadata, "deployment_status", "id")
	case "status":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "updated_at")
		id, iOk = shared.LookupMap[string](metadata, "id")
	case "release":
		{
			publishedAt, pOk := shared.LookupMap[string](metadata, "release", "published_at")
			if pOk {
				maybeTimeCreated = publishedAt
				tOk = true
			} else {
				createdAt, cOk := shared.LookupMap[string](metadata, "release", "created_at")
				if cOk {
					maybeTimeCreated = createdAt
					tOk = true
				}
			}
		}
		id, iOk = shared.LookupMap[string](metadata, "release", "id")
	default:
		return nil, fmt.Errorf("event type %s is not supported", eventType)
	}
	if !tOk || !iOk {
		return nil, fmt.Errorf("could not find time_created or id")
	}

	timeCreated, err := time.Parse(time.RFC3339, maybeTimeCreated)
	if err != nil {
		return nil, fmt.Errorf("could not parse time_created: %s", err)
	}

	return &EventRecord{
		EventType:   eventType,
		Id:          id,
		Metadata:    string(rawMetadata),
		TimeCreated: timeCreated,
		Signature:   signature,
		MsgId:       reqMessage.Message.MessageId,
		Source:      source,
	}, nil
}
