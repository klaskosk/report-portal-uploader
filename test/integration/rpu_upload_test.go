//go:build integration

package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/klaskosk/report-portal-cli/pkg/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("rpu upload", func() {
	It("imports junit XML from TOML config and returns a launch URL", func(ctx SpecContext) {
		launchName := "rpu-upload-it-" + uuid.NewString()
		configPath := writeUploadConfig(gatewayHostname(), testProject, launchName)

		stdout, stderr, err := runRPU(
			"upload",
			"--config", configPath,
		)
		Expect(err).NotTo(HaveOccurred(), "rpu upload failed: %s", stderr)

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
		Expect(launch.Name).To(Equal(launchName), "expected launch name to match config")
	})

	It("applies lane properties from multi-source config", func(ctx SpecContext) {
		launchName := "rpu-upload-lanes-" + uuid.NewString()
		fixtureDir := GinkgoT().TempDir()

		gmPath := filepath.Join(fixtureDir, "gm_suite.xml")
		sharedPath := filepath.Join(fixtureDir, "shared_suite.xml")
		err := os.WriteFile(gmPath, []byte(`<testsuites><testsuite name="GM"><testcase classname="A" name="one"/></testsuite></testsuites>`), 0o644)
		Expect(err).NotTo(HaveOccurred(), "expected gm fixture write")
		err = os.WriteFile(sharedPath, []byte(`<testsuites><testsuite name="Shared"><testcase classname="B" name="two"/></testsuite></testsuites>`), 0o644)
		Expect(err).NotTo(HaveOccurred(), "expected shared fixture write")

		configPath := writeUploadConfigWithSources(
			gatewayHostname(),
			testProject,
			launchName,
			[]uploadSourceEntry{
				{Lane: "gm", Path: filepath.Join(fixtureDir, "gm_suite.xml")},
				{Lane: "", Path: filepath.Join(fixtureDir, "shared_suite.xml")},
			},
			"ci-lane",
		)

		_, stderr, err := runRPU("upload", "--config", configPath)
		Expect(err).NotTo(HaveOccurred(), "rpu upload failed: %s", stderr)

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

			foundLane := false
			foundSharedWithoutLane := false
			for _, item := range *items.Content {
				if item.Id == nil {
					continue
				}

				fullItem, getErr := rpClient.Project(testProject).Items().GetByID(ctx, *item.Id)
				if getErr != nil || fullItem.Attributes == nil {
					continue
				}

				for _, attr := range *fullItem.Attributes {
					if attr.Key != nil && *attr.Key == "ci-lane" && attr.Value == "gm" {
						foundLane = true
					}
				}

				if item.Name != nil && *item.Name == "Shared" {
					for _, attr := range *fullItem.Attributes {
						if attr.Key != nil && *attr.Key == "ci-lane" {
							foundSharedWithoutLane = true
						}
					}
				}
			}

			return foundLane && !foundSharedWithoutLane
		}).WithTimeout(2*time.Minute).WithPolling(2*time.Second).Should(BeTrue(), "expected gm lane property only on lane-tagged suite")
	})

	It("writes launch_url_file from config", func() {
		launchName := "rpu-upload-url-" + uuid.NewString()
		urlFile := filepath.Join(GinkgoT().TempDir(), "report-portal-launch.url")
		configPath := writeUploadConfigWithOutput(gatewayHostname(), testProject, launchName, urlFile)

		_, stderr, err := runRPU("upload", "--config", configPath)
		Expect(err).NotTo(HaveOccurred(), "rpu upload failed: %s", stderr)

		content, err := os.ReadFile(urlFile)
		Expect(err).NotTo(HaveOccurred(), "expected launch URL file")
		Expect(string(content)).To(MatchRegexp(`^https?://`), "expected launch URL in file")
	})
})

type uploadSourceEntry struct {
	Lane string
	Path string
}

func writeUploadConfig(hostname, project, launchName string) string {
	return writeUploadConfigWithSources(hostname, project, launchName, []uploadSourceEntry{
		{Path: repoPath("test", "testdata", "results", "simple.xml")},
	}, "ci-lane")
}

func writeUploadConfigWithOutput(hostname, project, launchName, launchURLFile string) string {
	dir := GinkgoT().TempDir()
	path := filepath.Join(dir, "upload.toml")
	payload := fmt.Sprintf(`[report_portal]
hostname = %q
project = %q

[launch]
name = %q

[[sources]]
path = %q

[output]
launch_url_file = %q
`, hostname, project, launchName, repoPath("test", "testdata", "results", "simple.xml"), launchURLFile)

	err := os.WriteFile(path, []byte(payload), 0o644)
	Expect(err).NotTo(HaveOccurred(), "failed to write upload config")

	return path
}

func writeUploadConfigWithSources(
	hostname, project, launchName string,
	sources []uploadSourceEntry,
	laneProperty string,
) string {
	dir := GinkgoT().TempDir()
	path := filepath.Join(dir, "upload.toml")

	payload := fmt.Sprintf(`[report_portal]
hostname = %q
project = %q

[launch]
name = %q

[processing]
lane_property = %q
`, hostname, project, launchName, laneProperty)

	for _, source := range sources {
		payload += fmt.Sprintf("\n[[sources]]\nlane = %q\npath = %q\n", source.Lane, source.Path)
	}

	err := os.WriteFile(path, []byte(payload), 0o644)
	Expect(err).NotTo(HaveOccurred(), "failed to write upload config")

	return path
}
