package metrics

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/spendesk/github-actions-exporter/pkg/config" // Your config package

	"github.com/google/go-github/v72/github" // <<< UPDATED to v72
)

// Helper to safely get string from pointer
func getSafeString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

// Helper to safely get int64 from pointer for IDs etc.
func getSafeInt64(i *int64) int64 {
	if i != nil {
		return *i
	}
	return 0
}

// Helper to safely get int from pointer
func getSafeInt(i *int) int {
	if i != nil {
		return *i
	}
	return 0
}

// getFieldValue extracts basic, direct fields from a WorkflowRun object.
// It uses the global 'workflows' cache for 'workflow_name'.
func getFieldValue(repoFullName string, run github.WorkflowRun, fieldName string) string {
	switch fieldName {
	case "repo":
		return repoFullName
	case "run_id":
		return strconv.FormatInt(getSafeInt64(run.ID), 10)
	case "node_id":
		return getSafeString(run.NodeID)
	case "head_branch":
		return getSafeString(run.HeadBranch)
	case "head_sha":
		return getSafeString(run.HeadSHA)
	case "path":
		return getSafeString(run.Path)
	case "run_number":
		return strconv.Itoa(getSafeInt(run.RunNumber))
	case "run_attempt":
		return strconv.Itoa(getSafeInt(run.RunAttempt))
	case "event":
		return getSafeString(run.Event)
	case "display_title":
		return getSafeString(run.DisplayTitle)
	case "status":
		return getSafeString(run.Status)
	case "conclusion":
		return getSafeString(run.Conclusion)
	case "workflow_id":
		return strconv.FormatInt(getSafeInt64(run.WorkflowID), 10)
	case "workflow_name": // Uses the global 'workflows' cache
		if repoWorkflows, repoCacheExists := workflows[repoFullName]; repoCacheExists {
			if wf, wfExists := repoWorkflows[getSafeInt64(run.WorkflowID)]; wfExists && wf != nil && wf.Name != nil {
				return *wf.Name
			}
		}
		// log.Printf("Workflow name not found in cache for repo '%s', workflow_id '%d'", repoFullName, getSafeInt64(run.WorkflowID))
		return "unknown_workflow_name" // Default if not found
	case "pr_number": // Primarily derived in main loop; this is a fallback if requested directly
		if len(run.PullRequests) > 0 && run.PullRequests[0] != nil && run.PullRequests[0].Number != nil {
			return strconv.Itoa(*run.PullRequests[0].Number)
		}
		return ""
	case "actor_login":
		if run.Actor != nil && run.Actor.Login != nil {
			return *run.Actor.Login
		}
		return ""
	case "triggering_actor_login":
		if run.TriggeringActor != nil && run.TriggeringActor.Login != nil {
			return *run.TriggeringActor.Login
		}
		return ""
	case "created_at_unix":
		if run.CreatedAt != nil && !run.CreatedAt.IsZero() {
			return strconv.FormatInt(run.CreatedAt.Time.Unix(), 10)
		}
		return "0"
	case "updated_at_unix":
		if run.UpdatedAt != nil && !run.UpdatedAt.IsZero() {
			return strconv.FormatInt(run.UpdatedAt.Time.Unix(), 10)
		}
		return "0"
	case "run_started_at_unix":
		if run.RunStartedAt != nil && !run.RunStartedAt.IsZero() {
			return strconv.FormatInt(run.RunStartedAt.Time.Unix(), 10)
		}
		return "0"
	// "derived_target_branch" and "derived_commit_pr_title" are handled by the caller.
	}
	// log.Printf("Field '%s' not handled by getFieldValue or is a derived field.", fieldName)
	return "" // Return empty for unhandled direct fields
}

// getWorkflowRunsToFetchFromRepo fetches workflow runs for a single repository
// based on the configured creation age lookback.
func getWorkflowRunsToFetchFromRepo(owner string, repoName string) []*github.WorkflowRun {
	fetchHours := config.Github.FetchMaxWorkflowCreationAgeHours
	if fetchHours <= 0 {
		fetchHours = 12 // Default to 12 hours if not configured or invalid
		// log.Printf("FetchMaxWorkflowCreationAgeHours not configured or invalid for %s/%s, defaulting to %d hours.", owner, repoName, fetchHours)
	}
	// Ensure fetchHours is negative for time.Add relative to Now()
	if fetchHours > 0 {
		fetchHours = -fetchHours
	}

	windowStart := time.Now().Add(time.Duration(fetchHours) * time.Hour).Format(time.RFC3339)
	// log.Printf("Fetching workflow runs for %s/%s created since %s", owner, repoName, windowStart)

	listOptions := &github.ListWorkflowRunsOptions{
		ListOptions: github.ListOptions{PerPage: 100}, // Maximize items per page
		Created:     ">=" + windowStart,              // Filter by creation date
	}

	var allRuns []*github.WorkflowRun
	for {
		runsResponse, httpResp, err := client.Actions.ListRepositoryWorkflowRuns(context.Background(), owner, repoName, listOptions)
		if rlErr, ok := err.(*github.RateLimitError); ok {
			log.Printf("ListRepositoryWorkflowRuns ratelimited for %s/%s. Pausing until %s", owner, repoName, rlErr.Rate.Reset.Time.String())
			time.Sleep(time.Until(rlErr.Rate.Reset.Time))
			continue // Retry current page
		} else if err != nil {
			log.Printf("ListRepositoryWorkflowRuns error for repo %s/%s: %v", owner, repoName, err)
			return allRuns // Return what was fetched successfully before the error
		}

		if runsResponse != nil && runsResponse.WorkflowRuns != nil {
			allRuns = append(allRuns, runsResponse.WorkflowRuns...)
		}

		if httpResp.NextPage == 0 {
			break
		}
		listOptions.Page = httpResp.NextPage
	}
	// log.Printf("Fetched %d workflow runs for %s/%s created since %s", len(allRuns), owner, repoName, windowStart)
	return allRuns
}

// getWorkflowRunsFromGithub is the main goroutine for fetching and processing workflow run metrics.
func getWorkflowRunsFromGithub() {
	if client == nil {
		log.Println("Error in getWorkflowRunsFromGithub: GitHub client is not initialized.")
		return
	}
	if len(repositories) == 0 {
		log.Println("No repositories configured; getWorkflowRunsFromGithub will not run.")
		return
	}

	// Cache the split field names from config for minor efficiency inside the loop.
	configuredFieldNames := strings.Split(config.WorkflowFields, ",")
	if len(configuredFieldNames) == 0 {
		log.Println("Error: config.WorkflowFields resulted in zero labels. Cannot proceed with getWorkflowRunsFromGithub.")
		return
	}


	refreshTicker := time.NewTicker(time.Duration(config.Github.Refresh) * time.Second)
	defer refreshTicker.Stop()

	for range refreshTicker.C {
		log.Printf("Starting workflow run collection cycle for %d repositories.", len(repositories))
		workflowRunStatusGauge.Reset() // Clear all previously set statuses for all series
		if config.Metrics.FetchWorkflowRunUsage && workflowRunDurationGauge != nil {
			workflowRunDurationGauge.Reset()
		}

		for _, repoFullName := range repositories {
			ownerAndRepo := strings.Split(repoFullName, "/")
			if len(ownerAndRepo) != 2 {
				log.Printf("Invalid repository format '%s' in getWorkflowRunsFromGithub. Skipping.", repoFullName)
				continue
			}
			owner, repoName := ownerAndRepo[0], ownerAndRepo[1]

			fetchedRuns := getWorkflowRunsToFetchFromRepo(owner, repoName)

			for _, run := range fetchedRuns {
				if run == nil || run.ID == nil { // Basic safety check
					continue
				}

				// --- Derive Complex Fields ---
				var derivedTargetBranch string
				event := getSafeString(run.Event)

				if event == "pull_request" && len(run.PullRequests) > 0 && run.PullRequests[0] != nil &&
					run.PullRequests[0].Base != nil && run.PullRequests[0].Base.Ref != nil {
					derivedTargetBranch = *run.PullRequests[0].Base.Ref
				} else if run.HeadBranch != nil {
					// For 'push', HeadBranch is the branch pushed to.
					// For 'workflow_dispatch', HeadBranch is the branch the workflow definition runs on.
					// The actual "target" for a dispatch might be an input, not directly in the run object.
					// HeadBranch is a reasonable default here.
					derivedTargetBranch = *run.HeadBranch
				}
				// If derivedTargetBranch is still empty, it will be an empty label.

				var derivedCommitPrTitle string
				if event == "pull_request" && len(run.PullRequests) > 0 && run.PullRequests[0] != nil &&
					run.PullRequests[0].Title != nil {
					derivedCommitPrTitle = *run.PullRequests[0].Title
				} else if run.DisplayTitle != nil && *run.DisplayTitle != "" { // Use DisplayTitle (v72) if available
					derivedCommitPrTitle = *run.DisplayTitle
				} else if run.HeadCommit != nil && run.HeadCommit.Message != nil {
					// Use the first line of the head commit message as a fallback
					messageLines := strings.SplitN(*run.HeadCommit.Message, "\n", 2)
					derivedCommitPrTitle = strings.TrimSpace(messageLines[0])
				}
				// If derivedCommitPrTitle is still empty, it will be an empty label.


				// --- Determine Numeric Status (based on run.Status and run.Conclusion) ---
				var numericStatus float64 = 99 // Default for unknown or other states
				runStatus := getSafeString(run.Status)
				runConclusion := getSafeString(run.Conclusion)

				if runStatus == "completed" {
					switch runConclusion {
					case "success": numericStatus = 1
					case "failure": numericStatus = 0
					case "cancelled": numericStatus = 5
					case "skipped": numericStatus = 2
					case "neutral": numericStatus = 6
					case "timed_out": numericStatus = 7
					default: numericStatus = 8 // Unknown conclusion for a completed run
					}
				} else if runStatus == "in_progress" || runStatus == "requested" || runStatus == "waiting" {
					numericStatus = 3
				} else if runStatus == "queued" {
					numericStatus = 4
				} else if runStatus == "action_required" { // GitHub AE status
					numericStatus = 9
				} else if runStatus == "stale" { // Workflow runs that have not been updated in 7 days.
					numericStatus = 10
				}
				// numericStatus will remain 99 if no specific mapping is found.

				// --- Construct Label Values in the exact order defined by config.WorkflowFields ---
				labelValues := make([]string, len(configuredFieldNames))
				for i, fieldName := range configuredFieldNames {
					var val string
					switch fieldName {
					case "derived_target_branch":
						val = derivedTargetBranch
					case "derived_commit_pr_title":
						val = derivedCommitPrTitle
					default:
						val = getFieldValue(repoFullName, *run, fieldName)
					}
					labelValues[i] = val
				}

				workflowRunStatusGauge.WithLabelValues(labelValues...).Set(numericStatus)

				// --- Handle Workflow Run Duration (if enabled) ---
				if config.Metrics.FetchWorkflowRunUsage && workflowRunDurationGauge != nil {
					var durationMs float64 = -1 // Default to -1 if not calculable/fetched

					// Attempt to get precise duration from API first
					// Note: GetWorkflowRunUsageByID can be rate-limited or return 404 if timing info not ready.
					runUsage, _, errUsage := client.Actions.GetWorkflowRunUsageByID(context.Background(), owner, repoName, getSafeInt64(run.ID))
					if errUsage == nil && runUsage != nil && runUsage.RunDurationMS != nil {
						durationMs = float64(getSafeInt64(runUsage.RunDurationMS))
					} else {
						// Fallback: Use RunStartedAt and UpdatedAt (if status is completed/terminal)
						// This is less accurate, especially for re-runs or if UpdatedAt changes for other reasons.
						if (runStatus == "completed" || runStatus == "stale") && // Only for terminal states
							run.RunStartedAt != nil && !run.RunStartedAt.IsZero() &&
							run.UpdatedAt != nil && !run.UpdatedAt.IsZero() {
							if run.UpdatedAt.Time.After(run.RunStartedAt.Time) { // Sanity check
								durationMs = float64(run.UpdatedAt.Time.Sub(run.RunStartedAt.Time).Milliseconds())
							}
						}
						// Optionally log GetWorkflowRunUsageByID error if it wasn't a simple 404 (not ready)
						// if errUsage != nil && !strings.Contains(errUsage.Error(), "404") {
						// log.Printf("GetWorkflowRunUsageByID error for run %d (%s/%s): %v. Used fallback duration.", getSafeInt64(run.ID), owner, repoName, errUsage)
						// }
					}
					// Uses the same labelValues as workflowRunStatusGauge.
					// If the duration gauge needs different labels, this part needs adjustment.
					workflowRunDurationGauge.WithLabelValues(labelValues...).Set(durationMs)
				}
			} // End loop through runs for a repo
		} // End loop through repositories
		log.Printf("Finished workflow run collection cycle.")
	} // End ticker loop
}