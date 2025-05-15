package metrics

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/go-github/v72/github" // Ensure this is v72

	"github.com/spendesk/github-actions-exporter/pkg/config"
)

// NOTE: The global 'repositories' and 'workflows' are now declared in metrics.go
// This file will UPDATE those global variables.

func getAllReposForOrg(orga string) []string {
	if client == nil { // client is the global from metrics.go
		log.Printf("GitHub client not initialized in getAllReposForOrg for orga %s", orga)
		return nil
	}
	var allRepos []string // Renamed to avoid confusion if there was a global with same name locally

	opt := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{
			PerPage: 100, // Maximize items
		},
	}
	log.Printf("Fetching repositories for organization: %s", orga)
	for {
		reposPage, resp, err := client.Repositories.ListByOrg(context.Background(), orga, opt)
		if rlErr, ok := err.(*github.RateLimitError); ok {
			log.Printf("ListByOrg ratelimited for %s. Pausing until %s", orga, rlErr.Rate.Reset.Time.String())
			time.Sleep(time.Until(rlErr.Rate.Reset.Time))
			continue
		} else if err != nil {
			log.Printf("ListByOrg error for organization %s: %s", orga, err.Error())
			break // Stop for this org on error
		}

		for _, repo := range reposPage {
			if repo != nil && repo.FullName != nil {
				allRepos = append(allRepos, *repo.FullName)
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = resp.NextPage
	}
	log.Printf("Fetched %d repositories for organization: %s", len(allRepos), orga)
	return allRepos
}

// getAllWorkflowsForRepo fetches workflow definitions for a single repository.
// It now returns a map with pointers to github.Workflow.
func getAllWorkflowsForRepo(owner string, repoName string) map[int64]*github.Workflow {
	if client == nil { // client is the global from metrics.go
		log.Printf("GitHub client not initialized in getAllWorkflowsForRepo for %s/%s", owner, repoName)
		return nil
	}
	res := make(map[int64]*github.Workflow)

	opt := &github.ListOptions{
		PerPage: 100, // Maximize items
	}

	// log.Printf("Fetching workflow definitions for %s/%s", owner, repoName)
	for {
		workflowsPage, resp, err := client.Actions.ListWorkflows(context.Background(), owner, repoName, opt)
		if rlErr, ok := err.(*github.RateLimitError); ok {
			log.Printf("ListWorkflows ratelimited for %s/%s. Pausing until %s", owner, repoName, rlErr.Rate.Reset.Time.String())
			time.Sleep(time.Until(rlErr.Rate.Reset.Time))
			continue
		} else if err != nil {
			log.Printf("ListWorkflows error for %s/%s: %s", owner, repoName, err.Error())
			return res // Return what we have so far for this repo
		}

		if workflowsPage != nil && workflowsPage.Workflows != nil {
			for _, w := range workflowsPage.Workflows {
				if w != nil && w.ID != nil { // Ensure workflow and its ID are not nil
					res[*w.ID] = w // Store pointer to workflow
				}
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	// log.Printf("Fetched %d workflow definitions for %s/%s", len(res), owner, repoName)
	return res
}

// periodicGithubFetcher is intended to be run as a goroutine.
// It updates the global 'repositories' and 'workflows' variables.
func periodicGithubFetcher() {
	if client == nil {
		log.Println("GitHub client not initialized at start of periodicGithubFetcher. Will retry.")
	}

	// Determine refresh interval for this fetcher.
	// This might be different from the main GITHUB_REFRESH for workflow runs.
	// Using WorkflowCacheRefreshIntervalSeconds as it's for workflow definitions.
	refreshIntervalSeconds := config.Github.WorkflowCacheRefreshIntervalSeconds
	if refreshIntervalSeconds <= 0 {
		refreshIntervalSeconds = 3600 // Default to 1 hour
		log.Printf("periodicGithubFetcher: WorkflowCacheRefreshIntervalSeconds not configured or invalid, defaulting to %ds.", refreshIntervalSeconds)
	}
	if refreshIntervalSeconds < 60 {
		refreshIntervalSeconds = 60 // Minimum sensible interval
	}
	log.Printf("periodicGithubFetcher will refresh repositories and workflow definitions every %d seconds.", refreshIntervalSeconds)
	ticker := time.NewTicker(time.Duration(refreshIntervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		if client == nil { // Re-check client in loop in case it was initialized late
			log.Println("periodicGithubFetcher: GitHub client still not initialized. Sleeping.")
			time.Sleep(60 * time.Second) // Wait before retrying client check
			continue
		}

		log.Println("periodicGithubFetcher: Starting data refresh cycle...")
		var reposToProcess []string
		// Prioritize explicitly listed repositories
		if config.Github.Repositories.Value() != nil && len(config.Github.Repositories.Value()) > 0 {
			reposToProcess = config.Github.Repositories.Value()
			log.Printf("periodicGithubFetcher: Using %d explicitly configured repositories.", len(reposToProcess))
		} else if config.Github.Organizations.Value() != nil && len(config.Github.Organizations.Value()) > 0 {
			log.Printf("periodicGithubFetcher: No explicit repositories configured, discovering from %d organization(s).", len(config.Github.Organizations.Value()))
			for _, orga := range config.Github.Organizations.Value() {
				if orga != "" { // Ensure org name is not empty
					reposToProcess = append(reposToProcess, getAllReposForOrg(orga)...)
				}
			}
			log.Printf("periodicGithubFetcher: Discovered %d repositories from organizations.", len(reposToProcess))
		} else {
			log.Println("periodicGithubFetcher: No repositories or organizations configured. Nothing to fetch.")
			// Update globals to be empty to reflect this state
			// Consider if lock is needed if other goroutines read these during assignment
			// For simple assignment of the whole map/slice, it's often okay.
			repositories = []string{}
			workflows = make(map[string]map[int64]*github.Workflow)
			<-ticker.C // Wait for next tick
			continue
		}

		// Deduplicate repositories list (if an org repo was also listed explicitly)
		// This is a simple deduplication. For very large lists, more efficient methods exist.
		uniqueReposMap := make(map[string]bool)
		var uniqueReposList []string
		for _, repoFullName := range reposToProcess {
			if !uniqueReposMap[repoFullName] {
				uniqueReposMap[repoFullName] = true
				uniqueReposList = append(uniqueReposList, repoFullName)
			}
		}
		// Update the global 'repositories' slice
		// Consider mutex protection if other goroutines iterate over 'repositories' concurrently
		// with this assignment. For now, direct assignment.
		repositories = uniqueReposList
		log.Printf("periodicGithubFetcher: Processing %d unique repositories.", len(repositories))

		// Fetch workflows for the final list of repositories
		newWorkflowsData := make(map[string]map[int64]*github.Workflow)
		for _, repoFullName := range repositories { // Use the now updated global 'repositories'
			ownerAndRepo := strings.Split(repoFullName, "/")
			if len(ownerAndRepo) != 2 {
				log.Printf("periodicGithubFetcher: Invalid repository format '%s'. Skipping workflow fetch.", repoFullName)
				continue
			}
			owner, repoName := ownerAndRepo[0], ownerAndRepo[1]

			workflowsForRepo := getAllWorkflowsForRepo(owner, repoName)
			if len(workflowsForRepo) > 0 { // Only add if there are workflows
				newWorkflowsData[repoFullName] = workflowsForRepo
				// log.Printf("periodicGithubFetcher: Fetched %d workflows for %s", len(workflowsForRepo), repoFullName)
			}
		}

		// Atomically update the global 'workflows' map (or use a mutex)
		workflows = newWorkflowsData
		log.Printf("periodicGithubFetcher: Workflow definitions cache updated. Repos with workflows: %d. Total unique repos monitored: %d", len(workflows), len(repositories))

		<-ticker.C // Wait for the next tick
	}
}