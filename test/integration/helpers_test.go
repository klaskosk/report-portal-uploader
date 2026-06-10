//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/klaskosk/report-portal-cli/pkg/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

func obtainAPIToken(ctx context.Context, rpBaseURL, username, password string) (string, error) {
	tokenURL := rpBaseURL + "/uat/sso/oauth/token"
	form := url.Values{
		"grant_type": {"password"},
		"username":   {username},
		"password":   {password},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("ui", "uiman")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf("token request failed: %d %s", resp.StatusCode, string(body))
	}

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResponse); err != nil {
		return "", err
	}

	return tokenResponse.AccessToken, nil
}

func newClient() *client.Client {
	GinkgoHelper()

	c, err := client.New(baseURL, bearerToken)
	Expect(err).NotTo(HaveOccurred(), "failed to create client")

	return c
}

func repoPath(parts ...string) string {
	return filepath.Join(append([]string{"..", ".."}, parts...)...)
}

func writeIntegrationMetadata(hostname, project, launchName string, updaterDisabled bool) string {
	return writeIntegrationMetadataWithFilter(hostname, project, launchName, updaterDisabled, []string{"ci-lane"})
}

func writeIntegrationMetadataWithFilter(
	hostname, project, launchName string,
	updaterDisabled bool,
	propertyFilter []string,
) string {
	filterJSON, err := json.Marshal(propertyFilter)
	Expect(err).NotTo(HaveOccurred(), "failed to marshal property filter")

	payload := fmt.Sprintf(`{
  "targets": {
    "reportportal": {
      "disabled": false,
      "config": {"hostname": %q, "project": %q},
      "processing": {
        "disable_testitem_updater": %t,
        "property_filter": %s,
        "launch": {"name": %q, "description": "integration test"}
      }
    }
  }
}`, hostname, project, updaterDisabled, string(filterJSON), launchName)

	dir := GinkgoT().TempDir()
	path := filepath.Join(dir, "metadata.json")
	err = os.WriteFile(path, []byte(payload), 0o644)
	Expect(err).NotTo(HaveOccurred(), "failed to write metadata")

	return path
}

func runRPU(args ...string) (string, string, error) {
	GinkgoHelper()

	cmdArgs := append([]string{
		"--token", bearerToken,
		"-o", "json",
	}, args...)

	cmd := exec.Command(rpuBinaryPath, cmdArgs...)
	cmd.Env = append(os.Environ(), "REPORTPORTAL_TOKEN="+bearerToken)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

func gatewayHostname() string {
	return strings.TrimSuffix(baseURL, "/")
}
