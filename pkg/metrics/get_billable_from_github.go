package metrics

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/spendesk/github-actions-exporter/pkg/config" // Your config package

	"github.com/google/go-github/v72/github" // <<< UPDATED to v72
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// workflowBillGauge is defined here. Ensure its labels match how you intend to use it.
	// Current labels: "repo", "id", "node_id", "name", "state", "os"
	workflowBillGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_workflow_usage_seconds", // Consider if this should be workflow_run_usage_seconds for clarity
			Help: "Number of billable seconds used by a specific workflow RUN for a given OS during the current billing cycle. " +
				"Only applies to workflows in private repositories that use GitHub-hosted runners.",
		},
		[]string{"repo", "workflow_id", "workflow_node_id", "workflow_name", "workflow_state", "os_type"}, // Adjusted label names for clarity
	)
)

// getBillableFromGithub fetches billable information for workflow runs.
// Note: This function iterates through the 'workflows' cache, which contains workflow definitions,
// not workflow runs. To get billing per *run*, you'd typically iterate through runs.
// However, GetWorkflowUsageByID is for a specific *workflow definition ID*, not a run ID.
// The API endpoint is /repos/{owner}/{repo}/actions/workflows/{workflow_id}/timing
// This gets timing for the workflow definition, not an individual run.
// If you need billable time per RUN, you'd use GetWorkflowRunUsageByID (which you did in get_workflow_runs_from_github.go).
//
// Re-evaluating the purpose: The original code uses GetWorkflowUsageByID, which takes a *workflow_id*.
// This suggests the metric is per *workflow definition* and not per *workflow run*.
// The labels "id", "node_id", "name", "state" refer to the *workflow definition*.
func getBillableFromGithub() {
	if client == nil {
		log.Println("getBillableFromGithub: GitHub client not initialized.")
		return
	}
	if workflowBillGauge == nil { // Check if gauge was initialized (e.g. if this is conditionally run)
		log.Println("getBillableFromGithub: workflowBillGauge is not initialized.")
		return
	}

	// This ticker should probably be distinct from the main GITHUB_REFRESH if billing data updates less frequently.
	// For now, using a multiplier as in the original.
	refreshInterval := time.Duration(config.Github.Refresh) * 5 * time.Second
	if config.Github.Refresh <= 0 { // Fallback if config.Github.Refresh is not set
		refreshInterval = 300 * time.Second
	}
	log.Printf("getBillableFromGithub will refresh every %v", refreshInterval)
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		if len(workflows) == 0 || len(repositories) == 0 {
			// log.Println("getBillableFromGithub: No workflows or repositories cached/configured. Skipping cycle.")
			continue
		}

		log.Println("getBillableFromGithub: Starting billing collection cycle...")
		// It's good practice to Reset if the set of things you're reporting on might change,
		// or if some OS types might disappear for a workflow.
		workflowBillGauge.Reset()

		for repoFullName, repoWorkflowsMap := range workflows { // Iterate through cached workflows
			if repoWorkflowsMap == nil {
				continue
			}
			ownerAndRepo := strings.Split(repoFullName, "/")
			if len(ownerAndRepo) != 2 {
				log.Printf("getBillableFromGithub: Invalid repository format '%s'. Skipping.", repoFullName)
				continue
			}
			owner, repoName := ownerAndRepo[0], ownerAndRepo[1]

			for workflowID, workflowDefinition := range repoWorkflowsMap {
				if workflowDefinition == nil || workflowDefinition.ID == nil || workflowDefinition.Name == nil || workflowDefinition.NodeID == nil || workflowDefinition.State == nil {
					log.Printf("getBillableFromGithub: Incomplete workflow definition for ID %d in repo %s. Skipping.", workflowID, repoFullName)
					continue
				}

				// API call is client.Actions.GetWorkflowUsageByID(ctx, owner, repo, workflowID)
				// The original code had an inner loop for retries, which is good.
				var usageData *github.WorkflowUsage
				var errApi error
				for i := 0; i < 3; i++ { // Retry loop for API call
					usageData, _, errApi = client.Actions.GetWorkflowUsageByID(context.Background(), owner, repoName, workflowID)
					if rlErr, ok := errApi.(*github.RateLimitError); ok {
						log.Printf("GetWorkflowUsageByID ratelimited for workflow %d (%s/%s). Pausing until %s (attempt %d)", workflowID, owner, repoName, rlErr.Rate.Reset.Time.String(), i+1)
						time.Sleep(time.Until(rlErr.Rate.Reset.Time))
						continue // Retry API call
					} else if errApi != nil {
						log.Printf("GetWorkflowUsageByID error for workflow %d (%s/%s): %v (attempt %d)", workflowID, owner, repoName, errApi, i+1)
						// Don't break immediately, allow retries. If all retries fail, usageData will be nil.
					} else {
						break // Success
					}
					time.Sleep(2 * time.Second) // Small delay before retrying non-rate-limit errors
				}

				if errApi != nil || usageData == nil { // If all retries failed or usageData is nil
					log.Printf("Failed to get usage data for workflow %d (%s/%s) after retries.", workflowID, owner, repoName)
					continue // Skip to next workflow definition
				}

				billMap := usageData.GetBillable() // This is *github.WorkflowBillMap
				if billMap == nil || *billMap == nil { // Check if the map pointer or the map itself is nil
					// log.Printf("No billable data found for workflow %d (%s/%s).", workflowID, owner, repoName)
					continue
				}

				// Iterate over the OS types present in the billable map
				for osType, billData := range *billMap { // Dereference billMap to range over it
					if billData != nil && billData.TotalMS != nil {
						totalMs := getSafeInt64(billData.TotalMS) // Use helper for safety, though TotalMS is int64*
						workflowBillGauge.WithLabelValues(
							repoFullName,
							strconv.FormatInt(*workflowDefinition.ID, 10),
							*workflowDefinition.NodeID,
							*workflowDefinition.Name,
							*workflowDefinition.State,
							strings.ToUpper(osType), // Use the key from the map as the OS type
						).Set(float64(totalMs) / 1000) // Convert ms to seconds
					}
				}
			} // End loop through workflow definitions in a repo
		} // End loop through repositories in the workflows cache
		log.Println("getBillableFromGithub: Finished billing collection cycle.")
	} // End ticker loop
}

// getSafeInt64 helper (if not already present or imported from another file in the package)
// func getSafeInt64(i *int64) int64 {
// 	if i != nil {
// 		return *i
// 	}
// 	return 0 // Or some other indicator of nil, if 0 is a valid value
// }