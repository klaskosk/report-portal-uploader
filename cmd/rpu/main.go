package main

import (
	"fmt"
	"os"

	"github.com/klaskosk/report-portal-uploader/internal/cli"
)

func main() {
	err := cli.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
