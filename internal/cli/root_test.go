package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/klaskosk/report-portal-uploader/internal/cli"
)

func TestExecute_rejectsUnknownWaitFlag(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer

	err := cli.ExecuteWithArgs([]string{
		"send",
		"--metadata", "m.json",
		"--results", "r.xml",
		"--wait",
	}, &errOut)
	if err == nil {
		t.Fatal("expected error for --wait flag")
	}

	combined := err.Error() + errOut.String()
	if !strings.Contains(combined, "--wait") {
		t.Fatalf("expected --wait in error, got: %s", combined)
	}
}

func TestExecute_rejectsUnknownURLFlag(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer

	err := cli.ExecuteWithArgs([]string{
		"send",
		"--metadata", "m.json",
		"--results", "r.xml",
		"--url", "http://example.com",
	}, &errOut)
	if err == nil {
		t.Fatal("expected error for --url flag")
	}

	combined := err.Error() + errOut.String()
	if !strings.Contains(combined, "--url") {
		t.Fatalf("expected --url in error, got: %s", combined)
	}
}
