//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/klaskosk/report-portal-cli/pkg/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go/modules/compose"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/klaskosk/report-portal-uploader/test/integration/internal/runid"
)

const (
	testAdminUser     = "superadmin"
	testAdminPassword = "test-password"
	testProject       = "superadmin_personal"
)

var (
	baseURL      string
	bearerToken  string
	rpuBinaryPath string
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RPU Integration Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	stackID := runid.Generate()
	Expect(os.Setenv("RP_TEST_STACK_ID", stackID)).To(Succeed(), "failed to set RP_TEST_STACK_ID")

	composeFile := filepath.Join(".", "docker-compose.test.yml")
	stack, err := compose.NewDockerComposeWith(
		compose.WithStackFiles(composeFile),
		compose.StackIdentifier(stackID),
	)
	Expect(err).NotTo(HaveOccurred(), "failed to create compose stack")

	DeferCleanup(func(ctx SpecContext) {
		err := stack.Down(ctx, compose.RemoveOrphans(true), compose.RemoveVolumes(true))
		Expect(err).NotTo(HaveOccurred(), "failed to tear down compose stack")
	}, NodeTimeout(30*time.Second))

	err = stack.WithOsEnv().
		WaitForService("api", wait.ForLog("Started ReportPortalApp").WithStartupTimeout(5*time.Minute)).
		WaitForService("uat", wait.ForLog("Started").WithStartupTimeout(5*time.Minute)).
		Up(ctx)
	if err != nil {
		dumpServiceLogs(ctx, stack, "api", "migrations", "gateway")
	}
	Expect(err).To(Succeed(), "failed to start compose stack")

	gatewayContainer, err := stack.ServiceContainer(ctx, "gateway")
	Expect(err).NotTo(HaveOccurred(), "failed to get gateway container")

	mappedPort, err := gatewayContainer.MappedPort(ctx, "8080/tcp")
	Expect(err).NotTo(HaveOccurred(), "failed to get gateway port")

	host, err := gatewayContainer.Host(ctx)
	Expect(err).NotTo(HaveOccurred(), "failed to get gateway host")

	baseURL = fmt.Sprintf("http://%s:%s", host, mappedPort.Port())

	Eventually(func() error {
		bearerToken, err = obtainAPIToken(ctx, baseURL, testAdminUser, testAdminPassword)

		return err
	}).WithTimeout(60*time.Second).WithPolling(2*time.Second).Should(Succeed(), "failed to obtain API token")

	Eventually(func() error {
		probeClient, probeErr := client.New(baseURL, bearerToken)
		if probeErr != nil {
			return probeErr
		}

		_, probeErr = probeClient.Users().GetMyself(ctx)

		return probeErr
	}).WithTimeout(60*time.Second).WithPolling(2*time.Second).Should(Succeed(), "API readiness probe failed")

	rpuBinaryPath, err = buildRPUBinary()
	Expect(err).NotTo(HaveOccurred(), "failed to build rpu binary")

	DeferCleanup(func() {
		if rpuBinaryPath != "" {
			os.RemoveAll(filepath.Dir(rpuBinaryPath))
		}
	})
})

func dumpServiceLogs(ctx context.Context, stack *compose.DockerCompose, services ...string) {
	for _, svc := range services {
		container, err := stack.ServiceContainer(ctx, svc)
		if err != nil {
			GinkgoWriter.Printf("[%s] could not get container: %v\n", svc, err)

			continue
		}

		logs, err := container.Logs(ctx)
		if err != nil {
			GinkgoWriter.Printf("[%s] could not get logs: %v\n", svc, err)

			continue
		}

		body, _ := io.ReadAll(logs)
		logs.Close()
		GinkgoWriter.Printf("=== Logs for %s ===\n%s\n", svc, string(body))
	}
}

func buildRPUBinary() (string, error) {
	tmpDir, err := os.MkdirTemp("", "rpu-integration-*")
	if err != nil {
		return "", err
	}

	binaryPath := filepath.Join(tmpDir, "rpu")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/rpu")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Stdout = GinkgoWriter
	cmd.Stderr = GinkgoWriter

	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)

		return "", err
	}

	return binaryPath, nil
}
