package testframework

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// BuildGoBinary builds a go binary for linux/amd64 and returns its path.
func BuildGoBinary(t *testing.T, sourcePath string, outputName string) error {
	cmd := exec.Command("go", "build", "-o", outputName, sourcePath)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build go binary: %v, output: %s", err, string(out))
	}
	return nil
}

// BuildGoTestBinary builds a go test binary for linux/amd64 and returns its path.
func BuildGoTestBinary(t *testing.T, pkgPath string, outputName string) error {
	cmd := exec.Command("go", "test", "-c", "-o", outputName, pkgPath)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to build test binary: %v, output: %s", err, string(out))
	}
	return nil
}
