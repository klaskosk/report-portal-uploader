//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/klaskosk/report-portal-cli/pkg/client"
	"github.com/klaskosk/report-portal-cli/pkg/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("rpu send ginkgo post-import", func() {
	It("applies inventory attributes to testcase items not the suite", func(ctx SpecContext) {
		launchName := "rpu-ginkgo-" + uuid.NewString()
		metadataPath := writeIntegrationMetadataWithFilter(
			gatewayHostname(),
			testProject,
			launchName,
			false,
			[]string{"inventory-.*", "ci-lane"},
		)
		resultsGlob := repoPath("test", "testdata", "results", "ginkgo_example_suite.xml")

		stdout, stderr, err := runRPU(
			"send",
			"--metadata", metadataPath,
			"--results", resultsGlob,
		)
		Expect(err).NotTo(HaveOccurred(), "rpu send failed: %s", stderr)

		var result struct {
			LaunchID  int64    `json:"launchId"`
			Warnings  []string `json:"warnings"`
		}
		err = json.Unmarshal([]byte(stdout), &result)
		Expect(err).NotTo(HaveOccurred(), "expected JSON output")

		for _, warning := range result.Warnings {
			Expect(warning).NotTo(ContainSubstring("No Report Portal item matched"),
				"expected post-import matching to succeed")
		}
		for _, line := range strings.Split(stderr, "\n") {
			Expect(line).NotTo(ContainSubstring("No Report Portal item matched"),
				"expected no post-import matching warnings on stderr")
		}

		expected := map[string]string{
			"[It] verifies stability under load [stability, 10001, test_id:10001]":       "INV-10001",
			"[It] handles interface state changes [interfaces, 10002, test_id:10002]":    "INV-10002",
			"[It] applies log reduction settings [log-reduction, 10003, test_id:10003]": "INV-10003",
		}

		rpClient := newClient()
		Eventually(func(g Gomega) {
			items := listAllLaunchItems(ctx, rpClient, result.LaunchID)
			g.Expect(items).NotTo(BeEmpty(), "expected launch items")

			suite := findItemByName(items, "Example Ginkgo Suite")
			g.Expect(suite).NotTo(BeNil(), "expected suite item")
			g.Expect(itemAttributeValues(ctx, suite, "inventory-testcase-id")).To(BeEmpty(),
				"expected suite not to inherit testcase inventory attributes")

			for testName, inventoryID := range expected {
				testItem := findItemByName(items, testName)
				g.Expect(testItem).NotTo(BeNil(), "expected testcase item %q", testName)
				g.Expect(itemAttributeValues(ctx, testItem, "inventory-testcase-id")).To(
					ConsistOf(inventoryID),
					"expected inventory id on testcase %q", testName,
				)
			}
		}).WithTimeout(2*time.Minute).WithPolling(2*time.Second).Should(Succeed())
	})

	It("imports system-err logs onto the testcase step", func(ctx SpecContext) {
		launchName := "rpu-system-err-" + uuid.NewString()
		metadataPath := writeIntegrationMetadata(gatewayHostname(), testProject, launchName, true)
		resultsGlob := repoPath("test", "testdata", "results", "ginkgo_system_err_suite.xml")
		testName := "[It] captures stderr in junit output [stderr, 10004, test_id:10004]"

		stdout, stderr, err := runRPU(
			"send",
			"--metadata", metadataPath,
			"--results", resultsGlob,
		)
		Expect(err).NotTo(HaveOccurred(), "rpu send failed: %s", stderr)

		var result struct {
			LaunchID int64 `json:"launchId"`
		}
		err = json.Unmarshal([]byte(stdout), &result)
		Expect(err).NotTo(HaveOccurred(), "expected JSON output")

		rpClient := newClient()
		Eventually(func(g Gomega) {
			items := listAllLaunchItems(ctx, rpClient, result.LaunchID)
			stepItem := findItemByName(items, testName)
			g.Expect(stepItem).NotTo(BeNil(), "expected testcase step item")
			g.Expect(stepItem.Type).NotTo(BeNil(), "expected item type")
			g.Expect(*stepItem.Type).To(Equal("STEP"), "JUnit import maps testcase to STEP")
			g.Expect(stepItem.Id).NotTo(BeNil(), "expected item id")

			logs := listItemLogMessages(ctx, *stepItem.Id)
			g.Expect(logs).NotTo(BeEmpty(), "expected logs on testcase step")

			combined := strings.Join(logs, "\n")
			g.Expect(combined).To(ContainSubstring("STEP: preparing example fixture"),
				"expected BeforeEach step log from system-err")
			g.Expect(combined).To(ContainSubstring("STEP: verifying example output"),
				"expected It step log from system-err")
			g.Expect(combined).To(ContainSubstring("Executing query: example_metric"),
				"expected captured stderr output from system-err")
		}).WithTimeout(2*time.Minute).WithPolling(2*time.Second).Should(Succeed())
	})
})

func listAllLaunchItems(ctx SpecContext, rpClient *client.Client, launchID int64) []types.TestItemResource {
	GinkgoHelper()

	var all []types.TestItemResource
	page := int32(0)

	for {
		result, err := rpClient.Project(testProject).Items().List(
			ctx,
			client.WithItemLaunchID(launchID),
			client.WithItemPageNumber(page),
			client.WithItemPageSize(300),
		)
		Expect(err).NotTo(HaveOccurred(), "expected launch items list")
		if result.Content != nil {
			all = append(all, *result.Content...)
		}

		if result.Page == nil || result.Page.HasNext == nil || !*result.Page.HasNext {
			break
		}

		page++
	}

	return all
}

func findItemByName(items []types.TestItemResource, name string) *types.TestItemResource {
	for i := range items {
		if items[i].Name != nil && *items[i].Name == name {
			return &items[i]
		}
	}

	return nil
}

func listItemLogMessages(ctx SpecContext, itemID int64) []string {
	GinkgoHelper()

	rpClient := newClient()
	result, err := rpClient.Project(testProject).Logs().List(
		ctx,
		client.WithLogItemID(itemID),
		client.WithLogPageSize(300),
	)
	Expect(err).NotTo(HaveOccurred(), "expected item logs list")
	if result.Content == nil {
		return nil
	}

	var messages []string
	for _, entry := range *result.Content {
		if entry.Message != nil {
			messages = append(messages, *entry.Message)
		}
	}

	return messages
}

func itemAttributeValues(ctx context.Context, item *types.TestItemResource, key string) []string {
	if item == nil || item.Id == nil {
		return nil
	}

	rpClient := newClient()
	fullItem, err := rpClient.Project(testProject).Items().GetByID(ctx, *item.Id)
	Expect(err).NotTo(HaveOccurred(), "expected item fetch")
	if fullItem.Attributes == nil {
		return nil
	}

	var values []string
	for _, attr := range *fullItem.Attributes {
		if attr.Key != nil && *attr.Key == key {
			values = append(values, attr.Value)
		}
	}

	return values
}
