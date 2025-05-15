package metrics

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/spendesk/github-actions-exporter/pkg/config"

	"github.com/google/go-github/v72/github" // <<< Ensure v72
	"github.com/prometheus/client_golang/prometheus"
)

var (
	runnersOrganizationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "github_runner_organization_status",
			Help: "Organization runner status (1 for online, 0 for offline).",
		},
		[]string{"organization_name", "runner_os", "runner_name", "runner_id", "runner_busy"},
	)
)

func getAllOrgRunners(orgaName string) []*github.Runner {
	if client == nil {
		log.Println("getAllOrgRunners: GitHub client not initialized.")
		return nil
	}

	var allRunners []*github.Runner
	// CORRECTED: ListRunners and ListOrganizationRunners take *ListOptions in v72
	opt := &github.ListOptions{PerPage: 100} // Maximize items per page

	log.Printf("Fetching organization runners for %s", orgaName)
	for {
		runnersResponse, httpResp, err := client.Actions.ListOrganizationRunners(context.Background(), orgaName, opt)
		if rlErr, ok := err.(*github.RateLimitError); ok {
			log.Printf("ListOrganizationRunners ratelimited for org %s. Pausing until %s", orgaName, rlErr.Rate.Reset.Time.String())
			time.Sleep(time.Until(rlErr.Rate.Reset.Time))
			continue
		} else if err != nil {
			log.Printf("ListOrganizationRunners error for org %s: %v", orgaName, err)
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
	log.Printf("Fetched %d runners for organization %s", len(allRunners), orgaName)
	return allRunners
}

// getRunnersOrganizationFromGithub is the main goroutine for fetching organization-level runner metrics.
func getRunnersOrganizationFromGithub() {
	if client == nil {
		log.Println("getRunnersOrganizationFromGithub: GitHub client not initialized.")
		return
	}
	if runnersOrganizationGauge == nil {
		log.Println("getRunnersOrganizationFromGithub: runnersOrganizationGauge is not initialized.")
		return
	}
	// ... (rest of the function remains the same as the last version I provided for this file) ...
	if config.Github.Organizations.Value() == nil || len(config.Github.Organizations.Value()) == 0 {
		log.Println("getRunnersOrganizationFromGithub: No organizations configured. Skipping organization runner collection.")
		return
	}

	refreshInterval := time.Duration(config.Github.Refresh) * time.Second
	if config.Github.Refresh <= 0 {
		refreshInterval = 60 * time.Second
	}
	log.Printf("getRunnersOrganizationFromGithub will refresh every %v", refreshInterval)
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for range ticker.C {
		if config.Github.Organizations.Value() == nil || len(config.Github.Organizations.Value()) == 0 {
			continue
		}
		log.Printf("getRunnersOrganizationFromGithub: Starting organization runner collection cycle for %d organization(s).", len(config.Github.Organizations.Value()))
		runnersOrganizationGauge.Reset()

		for _, orgaName := range config.Github.Organizations.Value() {
			if orgaName == "" {
				continue
			}

			fetchedRunners := getAllOrgRunners(orgaName)
			if fetchedRunners == nil {
				continue
			}

			for _, runner := range fetchedRunners {
				if runner == nil || runner.ID == nil || runner.Name == nil || runner.OS == nil || runner.Status == nil || runner.Busy == nil {
					log.Printf("getRunnersOrganizationFromGithub: Incomplete runner data for an entry in org %s. Skipping.", orgaName)
					continue
				}

				var statusValue float64 = 0
				if runner.GetStatus() == "online" {
					statusValue = 1
				}

				runnersOrganizationGauge.WithLabelValues(
					orgaName,
					runner.GetOS(),
					runner.GetName(),
					strconv.FormatInt(runner.GetID(), 10),
					strconv.FormatBool(runner.GetBusy()),
				).Set(statusValue)
			}
		}
		log.Println("getRunnersOrganizationFromGithub: Finished organization runner collection cycle.")
	}
}
