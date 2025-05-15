package metrics

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/spendesk/github-actions-exporter/pkg/config"

	"github.com/google/go-github/v72/github" // <<< Ensure v72
	"github.com/prometheus/client_golang/prometheus"
)

var (
	runnersGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_status",
			Help: "Repository runner status (1 for online, 0 for offline).",
		},
		[]string{"repo_full_name", "runner_os", "runner_name", "runner_id", "runner_busy"},
	)
)

func getAllRepoRunners(owner string, repoName string) []*github.Runner {
	if client == nil {
		log.Println("getAllRepoRunners: GitHub client not initialized.")
		return nil
	}

	var allRunners []*github.Runner
	// CORRECTED: ListRunners and ListOrganizationRunners take *ListOptions in v72
	opt := &github.ListOptions{PerPage: 100} // Maximize items per page

	log.Printf("Fetching repository runners for %s/%s", owner, repoName)
	for {
		runnersResponse, httpResp, err := client.Actions.ListRunners(context.Background(), owner, repoName, opt)
		if rlErr, ok := err.(*github.RateLimitError); ok {
			log.Printf("ListRunners ratelimited for %s/%s. Pausing until %s", owner, repoName, rlErr.Rate.Reset.Time.String())
			time.Sleep(time.Until(rlErr.Rate.Reset.Time))
			continue
		} else if err != nil {
			log.Printf("ListRunners error for repo %s/%s: %v", owner, repoName, err)
			return allRunners
		}

		if runnersResponse != nil && runnersResponse.Runners != nil {
			allRunners = append(allRunners, runnersResponse.Runners...)
		}

		if httpResp.NextPage == 0 {
			break
		}
		opt.Page = httpResp.NextPage // ListOptions has a Page field
	}
	log.Printf("Fetched %d runners for repository %s/%s", len(allRunners), owner, repoName)
	return allRunners
}

// getRunnersFromGithub is the main goroutine for fetching repository-level runner metrics.
func getRunnersFromGithub() {
	if client == nil {
		log.Println("getRunnersFromGithub: GitHub client not initialized.")
		return
	}
	if runnersGauge == nil {
		log.Println("getRunnersFromGithub: runnersGauge is not initialized.")
		return
	}
	// ... (rest of the function remains the same as the last version I provided for this file) ...
	refreshInterval := time.Duration(config.Github.Refresh) * time.Second
	if config.Github.Refresh <= 0 {
		refreshInterval = 60 * time.Second // Default if not set
	}
	log.Printf("getRunnersFromGithub will refresh every %v", refreshInterval)
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		if len(repositories) == 0 {
			continue
		}
		log.Printf("getRunnersFromGithub: Starting repository runner collection cycle for %d repositories.", len(repositories))
		runnersGauge.Reset()

		for _, repoFullName := range repositories {
			ownerAndRepo := strings.Split(repoFullName, "/")
			if len(ownerAndRepo) != 2 {
				log.Printf("getRunnersFromGithub: Invalid repository format '%s'. Skipping.", repoFullName)
				continue
			}
			owner, repoName := ownerAndRepo[0], ownerAndRepo[1]

			fetchedRunners := getAllRepoRunners(owner, repoName)
			if fetchedRunners == nil {
				continue
			}

			for _, runner := range fetchedRunners {
				if runner == nil || runner.ID == nil || runner.Name == nil || runner.OS == nil || runner.Status == nil || runner.Busy == nil {
					log.Printf("getRunnersFromGithub: Incomplete runner data for an entry in %s. Skipping.", repoFullName)
					continue
				}

				var statusValue float64 = 0
				if runner.GetStatus() == "online" {
					statusValue = 1
				}

				runnersGauge.WithLabelValues(
					repoFullName,
					runner.GetOS(),
					runner.GetName(),
					strconv.FormatInt(runner.GetID(), 10),
					strconv.FormatBool(runner.GetBusy()),
				).Set(statusValue)
			}
		}
		log.Println("getRunnersFromGithub: Finished repository runner collection cycle.")
	}
}