//go:build integration

package integration_test

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/klaskosk/report-portal-cli/pkg/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("rpu send", func() {
	It("imports junit XML and returns a launch URL", func(ctx SpecContext) {
		launchName := "rpu-it-" + uuid.NewString()
		metadataPath := writeIntegrationMetadata(gatewayHostname(), testProject, launchName, true)
		resultsGlob := repoPath("test", "testdata", "results", "simple.xml")

		stdout, stderr, err := runRPU(
			"send",
			"--metadata", metadataPath,
			"--results", resultsGlob,
		)
		Expect(err).NotTo(HaveOccurred(), "rpu send failed: %s", stderr)

		var result struct {
			LaunchURL  string `json:"launchUrl"`
			LaunchID   int64  `json:"launchId"`
			LaunchUUID string `json:"launchUuid"`
		}
		err = json.Unmarshal([]byte(stdout), &result)
		Expect(err).NotTo(HaveOccurred(), "expected JSON output")
		Expect(result.LaunchURL).NotTo(BeEmpty(), "expected launch URL")
		Expect(result.LaunchID).To(BeNumerically(">", 0), "expected launch ID")

		rpClient := newClient()
		launch, err := rpClient.Project(testProject).Launches().GetByUUID(ctx, result.LaunchUUID)
		Expect(err).NotTo(HaveOccurred(), "expected launch to exist in RP")
		Expect(launch.Name).To(Equal(launchName), "expected launch name to match metadata")
	})

	It("applies item attributes when updater is enabled", func(ctx SpecContext) {
		launchName := "rpu-attrs-" + uuid.NewString()
		metadataPath := writeIntegrationMetadata(gatewayHostname(), testProject, launchName, false)
		resultsGlob := repoPath("test", "testdata", "results", "collision.xml")

		_, stderr, err := runRPU(
			"send",
			"--metadata", metadataPath,
			"--results", resultsGlob,
		)
		Expect(err).NotTo(HaveOccurred(), "rpu send failed: %s", stderr)

		rpClient := newClient()
		Eventually(func() bool {
			page, listErr := rpClient.Project(testProject).Launches().GetLatest(
				ctx,
				client.WithLaunchName(launchName),
			)
			if listErr != nil || page.Content == nil || len(*page.Content) == 0 {
				return false
			}

			launchID := (*page.Content)[0].Id
			items, listErr := rpClient.Project(testProject).Items().List(
				ctx,
				client.WithItemLaunchID(launchID),
				client.WithItemPageSize(300),
			)
			if listErr != nil || items.Content == nil {
				return false
			}

			for _, item := range *items.Content {
				if item.Id == nil {
					continue
				}

				fullItem, getErr := rpClient.Project(testProject).Items().GetByID(ctx, *item.Id)
				if getErr != nil || fullItem.Attributes == nil {
					continue
				}

				for _, attr := range *fullItem.Attributes {
					if attr.Key != nil && *attr.Key == "ci-lane" && attr.Value == "lane-a" {
						return true
					}
				}
			}

			return false
		}).WithTimeout(2*time.Minute).WithPolling(2*time.Second).Should(BeTrue(), "expected ci-lane attribute on item")
	})

	It("records import response shape with data.id", func(ctx SpecContext) {
		launchName := "rpu-shape-" + uuid.NewString()
		metadataPath := writeIntegrationMetadata(gatewayHostname(), testProject, launchName, true)
		resultsGlob := repoPath("test", "testdata", "results", "simple.xml")

		stdout, stderr, err := runRPU(
			"send",
			"--metadata", metadataPath,
			"--results", resultsGlob,
		)
		Expect(err).NotTo(HaveOccurred(), "rpu send failed: %s", stderr)
		Expect(stdout).To(ContainSubstring(`"launchUuid"`), "expected launch UUID in JSON output")
		Expect(stderr).NotTo(ContainSubstring("import response missing data.id"), "unexpected import parse failure")
	})
})
