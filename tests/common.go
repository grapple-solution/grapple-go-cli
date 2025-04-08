package tests

import (
	"os"
	"os/exec"
	"testing"

	"github.com/grapple-solution/grapple_cli/utils"
)

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCmdWithoutLogs(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}
func setFailed(t *testing.T) {
	err := os.WriteFile("/tmp/failed_flag", []byte("true"), 0644)
	if err != nil {
		t.Fatal(err)
	}
}

func checkPreviousTestFailed(t *testing.T) {

	data, err := os.ReadFile("/tmp/failed_flag")
	if err == nil && string(data) == "true" {
		utils.ErrorMessage("Skipping test because previous test civoTestFailed")
		t.Skip("Previous test civoTestFailed")
	}
}
