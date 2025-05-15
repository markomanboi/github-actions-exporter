package config

import "github.com/urfave/cli/v2"

var (
	// Github - github configuration
	Github struct {
		AppID                             int64  `split_words:"true"`
		AppInstallationID                 int64  `split_words:"true"`
		AppPrivateKey                     string `split_words:"true"`
		Token                             string
		Refresh                           int64 // Refresh time for main data fetching loop (workflow runs, etc.)
		Repositories                      cli.StringSlice
		Organizations                     cli.StringSlice // Note: Current code mainly uses Repositories directly for workflow runs. Org support would need expansion.
		APIURL                            string
		CacheSizeBytes                    int64
		FetchMaxWorkflowCreationAgeHours  int64 `mapstructure:"fetch_max_workflow_creation_age_hours"` // New: How far back to look for "created" workflow runs
		WorkflowCacheRefreshIntervalSeconds int64 `mapstructure:"workflow_cache_refresh_interval_seconds"` // New: How often to refresh workflow ID->name cache
	}
	Metrics struct {
		FetchWorkflowRunUsage bool
	}
	Port           int
	Debug          bool
	EnterpriseName string // Used for enterprise-specific runner/billing metrics, not directly for core workflow runs
	WorkflowFields string // Comma-separated list of labels for github_workflow_run_status
)

// InitConfiguration - set configuration from env vars or command parameters
func InitConfiguration() []cli.Flag {
	return []cli.Flag{
		&cli.Int64Flag{
			Name:        "app_id",
			Aliases:     []string{"gai"},
			EnvVars:     []string{"GITHUB_APP_ID"},
			Usage:       "Github App Id",
			Destination: &Github.AppID,
		},
		&cli.Int64Flag{
			Name:        "app_installation_id",
			Aliases:     []string{"gii"},
			EnvVars:     []string{"GITHUB_APP_INSTALLATION_ID"},
			Usage:       "Github App Installation Id",
			Destination: &Github.AppInstallationID,
		},
		&cli.StringFlag{
			Name:        "app_private_key",
			Aliases:     []string{"gpk"},
			EnvVars:     []string{"GITHUB_APP_PRIVATE_KEY"},
			Usage:       "Github App Private Key",
			Destination: &Github.AppPrivateKey,
		},
		&cli.IntFlag{
			Name:        "port",
			Aliases:     []string{"p"},
			EnvVars:     []string{"PORT"},
			Value:       9999,
			Usage:       "Exporter port",
			Destination: &Port,
		},
		&cli.StringFlag{
			Name:        "github_token",
			Aliases:     []string{"gt"},
			EnvVars:     []string{"GITHUB_TOKEN"},
			Usage:       "Github Personal Token",
			Destination: &Github.Token,
		},
		&cli.Int64Flag{
			Name:        "github_refresh",
			Aliases:     []string{"gr"},
			EnvVars:     []string{"GITHUB_REFRESH"},
			Value:       60, // Increased default, fetching many runs can be API intensive
			Usage:       "Refresh time for fetching workflow runs and other primary metrics in sec",
			Destination: &Github.Refresh,
		},
		&cli.StringFlag{
			Name:        "github_api_url",
			Aliases:     []string{"url"},
			EnvVars:     []string{"GITHUB_API_URL"},
			Value:       "api.github.com", // Keep default, user overrides for GHE
			Usage:       "Github API URL (e.g., https://github.example.com/api/v3 for GHE)",
			Destination: &Github.APIURL,
		},
		&cli.StringSliceFlag{
			Name:        "github_orgas",
			Aliases:     []string{"go"},
			EnvVars:     []string{"GITHUB_ORGAS"},
			Usage:       "List all organizations you want get informations. (Note: current workflow run fetching is repo-based)",
			Destination: &Github.Organizations,
		},
		&cli.StringSliceFlag{
			Name:        "github_repos",
			Aliases:     []string{"grs"},
			EnvVars:     []string{"GITHUB_REPOS"},
			Usage:       "List all repositories to monitor. Format <owner>/<repo>,<owner>/<repo2>",
			Destination: &Github.Repositories,
		},
		&cli.BoolFlag{
			Name:        "debug_profile",
			EnvVars:     []string{"DEBUG_PROFILE"},
			Usage:       "Expose pprof information on /debug/pprof/",
			Destination: &Debug,
		},
		&cli.StringFlag{
			Name:        "enterprise_name",
			EnvVars:     []string{"ENTERPRISE_NAME"},
			Usage:       "Enterprise name for enterprise-specific endpoints (runners, billing)",
			Destination: &EnterpriseName,
			Value:       "",
		},
		&cli.StringFlag{
			Name:    "export_fields", // Original name: "export_fields"
			EnvVars: []string{"EXPORT_FIELDS_WORKFLOW_RUN"}, // Changed EnvVar to be more specific
			Usage: "A comma-separated, ordered list of labels for github_workflow_run_status metric. " +
				"Order matters and must align with internal logic.",
			// Updated default value to reflect the new, richer set of fields.
			// Ensure this order is respected in getFieldValue and label construction.
			Value: "repo,workflow_id,workflow_name,run_id,run_number,run_attempt,event,status,conclusion,head_branch," +
				"derived_target_branch,pr_number,derived_commit_pr_title,display_title,actor_login,triggering_actor_login," +
				"created_at_unix,updated_at_unix,run_started_at_unix,path",
			Destination: &WorkflowFields,
		},
		&cli.BoolFlag{
			Name:        "fetch_workflow_run_usage",
			EnvVars:     []string{"FETCH_WORKFLOW_RUN_USAGE"},
			Usage:       "When true, will perform an API call per workflow run to fetch the workflow usage (duration)",
			Value:       true,
			Destination: &Metrics.FetchWorkflowRunUsage,
		},
		&cli.Int64Flag{
			Name:        "github_cache_size_bytes",
			EnvVars:     []string{"GITHUB_CACHE_SIZE_BYTES"},
			Value:       10 * 1024 * 1024, // Default 10MB, was 100MB, adjust as needed
			Usage:       "Size of Github HTTP cache in bytes",
			Destination: &Github.CacheSizeBytes,
		},
		// --- New Flags ---
		&cli.Int64Flag{
			Name:    "fetch_max_workflow_creation_age_hours",
			EnvVars: []string{"FETCH_MAX_WORKFLOW_CREATION_AGE_HOURS"},
			Value:   720, // Default to 30 days (30 * 24)
			Usage: "How far back in hours to look for workflow runs based on their CREATION time. " +
				"This defines the maximum age of runs the exporter will attempt to fetch.",
			Destination: &Github.FetchMaxWorkflowCreationAgeHours,
		},
		&cli.Int64Flag{
			Name:    "workflow_cache_refresh_interval_seconds",
			EnvVars: []string{"WORKFLOW_CACHE_REFRESH_INTERVAL_SECONDS"},
			Value:   3600, // Default to 1 hour
			Usage:   "How often in seconds to refresh the cache mapping workflow IDs to workflow names.",
			Destination: &Github.WorkflowCacheRefreshIntervalSeconds,
		},
	}
}