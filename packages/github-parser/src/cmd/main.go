package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
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

	http.HandleFunc("/", withLogger(withTraceId(index)))

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

func withTraceId(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logger := shared.LoggerFromContext(ctx)
		traceID, _ := extractTraceID([]byte(r.Header.Get("X-Cloud-Trace-Context")))
		nextLogger := logger.With(
			slog.String("logging.googleapis.com/trace", fmt.Sprintf("projects/%s/traces/%s", envVars.projectID, traceID)),
			slog.String("path", r.URL.Path),
		)
		shared.SetLogger(ctx, nextLogger)

		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

func extractTraceID(raw []byte) (string, bool) {
	if (len(raw)) == 0 {
		return "", false
	}

	// NOTE: https://cloud.google.com/trace/docs/setup?hl=ja#force-trace
	matches := regexp.MustCompile(`([a-f\d]+)/([a-f\d]+)`).FindAllSubmatch(raw, -1)
	if len(matches) != 1 {
		return "", false
	}

	sub := matches[0]
	if len(sub) != 3 {
		return "", false
	}

	return string(sub[1]), true
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

	{
		attrsStr, err := json.Marshal(attrs)
		if err != nil {
			logger.Error(fmt.Sprintf("error marshalling attrs: %s", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		metadataStr, err := json.Marshal(metadata)
		if err != nil {
			logger.Error(fmt.Sprintf("error marshalling metadata: %s", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		logger.Info("request attr", slog.String("attrs", string(attrsStr)))
		logger.Info("request metadata", slog.String("metadata", string(metadataStr)))
	}
	logger.Info("parsed",
		slog.Any("attr", attrs),
		slog.Any("metadata", metadata),
	)

	event, err := processGithubEvent(r.Context(), msg, attrs, metadata, data)
	if err != nil {
		logger.Warn(fmt.Sprintf("error processing github event: %s", err))
		w.WriteHeader(http.StatusOK)
		return
	}
	if event != nil {
		err = insertIntoBigQuery(r.Context(), event)
		if err != nil {
			logger.Warn(fmt.Sprintf("error inserting into bigquery: %s", err))
			w.WriteHeader(http.StatusOK)
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
		tErr, iErr           error
	)
	switch eventType {
	case "push":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "head_commit", "timestamp")
		id, iOk = shared.LookupMap[string](metadata, "head_commit", "id")
	case "pull_request":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "pull_request", "updated_at")
		{
			repoName, rOk := shared.LookupMap[string](metadata, "repository", "name")
			number, nOk := shared.LookupMap[float64](metadata, "number")
			if rOk && nOk {
				id = fmt.Sprintf("%s/%d", repoName, int(number))
				iOk = true
			}
		}
	case "pull_request_review":
		maybeTimeCreated, tErr = shared.LookupMapE[string](metadata, "review", "submitted_at")
		tOk = tErr == nil

		{
			idNum, nOk := shared.LookupMapE[float64](metadata, "review", "id")
			if nOk == nil {
				id = fmt.Sprintf("%d", int(idNum))
				iOk = true
			} else {
				iErr = nOk
			}
		}
	case "pull_request_review_comment":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "comment", "updated_at")
		{
			idNum, nOk := shared.LookupMap[float64](metadata, "review", "id")
			if nOk {
				id = fmt.Sprintf("%d", int(idNum))
				iOk = true
			}
		}
	case "issues":
		maybeTimeCreated, tOk = shared.LookupMap[string](metadata, "issue", "updated_at")
		{
			repoName, rOk := shared.LookupMap[string](metadata, "repository", "name")
			number, nOk := shared.LookupMap[float64](metadata, "issue", "number")
			if rOk && nOk {
				id = fmt.Sprintf("%s/%d", repoName, int(number))
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
		var errs []error
		if !tOk {
			if tErr == nil {
				errs = append(errs, fmt.Errorf("could not find time_created"))
			} else {
				errs = append(errs, tErr)
			}
		}

		if !iOk {
			if iErr == nil {
				errs = append(errs, fmt.Errorf("could not find id"))
			} else {
				errs = append(errs, iErr)
			}
		}
		return nil, errors.Join(errs...)
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
