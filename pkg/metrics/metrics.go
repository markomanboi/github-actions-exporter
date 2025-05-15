package metrics

import (
	"context" // Keep for client.Actions calls if any are directly in this file in future
	"fmt"
	"log"
	"net/http"
	// "net/url" // <<< REMOVE THIS LINE if getEnterpriseApiUrl helper is not used
	"strings"
	"time"

	"github.com/spendesk/github-actions-exporter/pkg/config"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/die-net/lrucache"
	"github.com/google/go-github/v72/github" // <<< ENSURE v72
	"github.com/gregjones/httpcache"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"
)

var (
	client *github.Client // Global GitHub client instance

	// Workflow Run Metrics
	workflowRunStatusGauge   *prometheus.GaugeVec
	workflowRunDurationGauge *prometheus.GaugeVec

	// Global cache for workflow definitions (ID to Name mapping)
	// Key: "owner/repo", Value: map[workflow_id]*github.Workflow
	// This is DECLARED HERE and UPDATED by functions in github_fetcher.go
	workflows map[string]map[int64]*github.Workflow = make(map[string]map[int64]*github.Workflow)

	// Slice of repositories to monitor, populated from config or discovered.
	// This is DECLARED HERE and UPDATED by functions in github_fetcher.go
	repositories []string

	// TODO: Define other gauges if you are using them (runnersGauge, etc.)
	// runnersGauge             *prometheus.GaugeVec
	// runnersOrganizationGauge *prometheus.GaugeVec
	// workflowBillGauge        *prometheus.GaugeVec // This would need its own fetcher logic
	// runnersEnterpriseGauge   *prometheus.GaugeVec
)

// InitMetrics initializes and registers Prometheus metrics and starts metric collection goroutines.
func InitMetrics() {
	// Note: 'repositories' slice is now populated by 'periodicGithubFetcher' initially.
	// 'InitMetrics' will set up gauges and start the goroutines.

	// --- Initialize Prometheus Gauges ---
	if config.WorkflowFields == "" {
		log.Fatalln("Error: Configuration 'WorkflowFields' (env: EXPORT_FIELDS_WORKFLOW_RUN) is empty. Cannot initialize workflow_run_status metric.")
	}
	workflowRunLabelNames := strings.Split(config.WorkflowFields, ",")

	workflowRunStatusGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_workflow_run_status",
			Help: "Status of GitHub Actions workflow runs. Fetches runs created within the 'fetch_max_workflow_creation_age_hours'. " +
				"Labels are defined by 'export_fields_workflow_run' config.",
		},
		workflowRunLabelNames,
	)
	prometheus.MustRegister(workflowRunStatusGauge)

	if config.Metrics.FetchWorkflowRunUsage {
		workflowRunDurationGauge = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "github_workflow_run_duration_ms",
				Help: "Duration of GitHub Actions workflow runs in milliseconds. Subject to the same fetching rules as run status.",
			},
			workflowRunLabelNames, // Assuming duration uses the same labels for simplicity
		)
		prometheus.MustRegister(workflowRunDurationGauge)
	}

	// TODO: Register other metrics if you use them

	// --- Initialize GitHub Client ---
	var clientErr error
	client, clientErr = NewClient() // 'client' is our global client
	if clientErr != nil {
		log.Fatalf("Error: GitHub client creation failed: %v", clientErr)
	}

	// --- Start Goroutines for Metric Collection ---
	// Start fetcher for repository list and workflow definitions (ID -> Name mapping)
	// This will also perform an initial fetch.
	go periodicGithubFetcher() // This function is now in github_fetcher.go

	// Optional: Wait for the first fetch of repositories and workflow definitions.
	// This helps ensure 'repositories' and 'workflows' have some data before 'getWorkflowRunsFromGithub' heavily relies on them.
	log.Println("Waiting briefly for initial repository and workflow definition fetch...")
	time.Sleep(10 * time.Second) // Adjust as needed, or implement a channel/waitgroup for true sync.

	// Start fetcher for workflow runs (the main data we're interested in)
	// getWorkflowRunsFromGithub will use the global 'repositories' list.
	go getWorkflowRunsFromGithub() // This function is in get_workflow_runs_from_github.go

	// TODO: Start other metric gathering goroutines if they exist (e.g., for billing, runners)
	// Example: if workflowBillGauge != nil { go getBillableFromGithub() }


	log.Println("GitHub Actions Exporter initialized and metrics collection started.")
}


// NewClient creates and configures a new GitHub API client. (Code from previous response, ensure it's up-to-date)
func NewClient() (*github.Client, error) {
	var httpClient *http.Client
	cacheSizeBytes := config.Github.CacheSizeBytes
	if cacheSizeBytes <= 0 {
		cacheSizeBytes = 10 * 1024 * 1024
	}
	lruCache := lrucache.New(cacheSizeBytes, 0)
	cachingTransport := httpcache.NewTransport(lruCache)
	baseTransport := http.RoundTripper(cachingTransport)

	if config.Github.Token != "" {
		log.Println("Authenticating with GitHub Token.")
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: config.Github.Token})
		authContext := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: baseTransport})
		httpClient = oauth2.NewClient(authContext, ts)
	} else if config.Github.AppID != 0 && config.Github.AppInstallationID != 0 && config.Github.AppPrivateKey != "" {
		log.Println("Authenticating with GitHub App.")
		appTransport, err := ghinstallation.NewKeyFromFile(baseTransport, config.Github.AppID, config.Github.AppInstallationID, config.Github.AppPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("GitHub App authentication setup failed: %w", err)
		}
		if config.Github.APIURL != "" && config.Github.APIURL != "api.github.com" {
			// Ensure config.Github.APIURL is the GHE API base, e.g., "https://my.ghe.com/api/v3"
			// The ghinstallation transport expects this to correctly form token URLs.
			appTransport.BaseURL = strings.TrimSuffix(config.Github.APIURL, "/")
			log.Printf("GitHub App transport BaseURL set for GHE: %s", appTransport.BaseURL)
		}
		httpClient = &http.Client{Transport: appTransport}
	} else {
		log.Println("No GitHub Token or App credentials provided. Using unauthenticated client (limited rate). Caching will still apply.")
		httpClient = &http.Client{Transport: baseTransport}
	}

	var ghClient *github.Client
	var errGHClient error
	if config.Github.APIURL != "" && config.Github.APIURL != "api.github.com" {
		log.Printf("Creating GitHub Enterprise client with API URL: %s", config.Github.APIURL)
		ghClient, errGHClient = github.NewEnterpriseClient(config.Github.APIURL, config.Github.APIURL, httpClient)
	} else {
		log.Println("Creating GitHub public API client.")
		ghClient = github.NewClient(httpClient)
	}
	if errGHClient != nil {
		return nil, fmt.Errorf("GitHub client creation failed: %w", errGHClient)
	}
	return ghClient, nil
}