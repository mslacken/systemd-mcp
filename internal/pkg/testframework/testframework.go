package testframework

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SetupPodmanEnv configures the environment to use podman for testcontainers.
func SetupPodmanEnv(t *testing.T) {
	if err := exec.Command("systemctl", "--user", "start", "podman.socket").Run(); err != nil {
		t.Logf("Failed to start podman.socket: %v (ignoring, as it might already be running or we might be in a different environment)", err)
	}

	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	if os.Getenv("DOCKER_HOST") == "" {
		os.Setenv("DOCKER_HOST", fmt.Sprintf("unix:///run/user/%d/podman/podman.sock", os.Getuid()))
	}
}

// StartKeycloakContainer starts a Keycloak container for testing OAuth2 authentication.
// It imports the provided realm configuration file.
func StartKeycloakContainer(ctx context.Context, t *testing.T, realmConfigPath string) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:        "quay.io/keycloak/keycloak:latest",
		ExposedPorts: []string{"8080/tcp"},
		Env: map[string]string{
			"KC_BOOTSTRAP_ADMIN_USERNAME": "admin",
			"KC_BOOTSTRAP_ADMIN_PASSWORD": "admin",
		},
		Cmd: []string{"start-dev", "--import-realm"},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      realmConfigPath,
				ContainerFilePath: "/opt/keycloak/data/import/realm.json",
				FileMode:          0644,
			},
		},
		// Wait for Keycloak's master realm to be available
		WaitingFor: wait.ForHTTP("/realms/master").WithPort("8080/tcp").WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
		ProviderType:     testcontainers.ProviderPodman,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start keycloak container: %w", err)
	}

	return container, nil
}

// StartSystemdContainer starts an openSUSE bci-init container with systemd as PID 1.
// It accepts optional files to inject into the container before starting.
func StartSystemdContainer(ctx context.Context, t *testing.T, files []testcontainers.ContainerFile) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:        "registry.opensuse.org/opensuse/bci/bci-init",
		Privileged:   true, // Required for systemd
		ExposedPorts: []string{"8080/tcp"},
		Files:        files,
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
		ProviderType:     testcontainers.ProviderPodman,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	// Wait for systemd to initialize
	var startErr error
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		_, _, err := container.Exec(ctx, []string{"systemctl", "daemon-reload"})
		if err == nil {
			startErr = nil
			break
		}
		startErr = fmt.Errorf("systemctl daemon-reload error: %v", err)
	}

	if startErr != nil {
		container.Terminate(context.Background())
		return nil, fmt.Errorf("systemd did not initialize in time: %v", startErr)
	}

	return container, nil
}

// WaitForService enables and starts a service inside the container, then waits for it.
func WaitForService(ctx context.Context, t *testing.T, container testcontainers.Container, serviceName string) error {
	var startErr error
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		_, outReader, err := container.Exec(ctx, []string{"systemctl", "enable", "--now", serviceName})
		if err == nil {
			startErr = nil
			break
		}

		var outStr string
		if outReader != nil {
			if b, readErr := io.ReadAll(outReader); readErr == nil {
				outStr = string(b)
			}
		}
		startErr = fmt.Errorf("systemctl enable error: %v, out: %s", err, outStr)
	}

	if startErr != nil {
		return startErr
	}

	// Give it a few seconds to start up completely
	time.Sleep(5 * time.Second)
	return nil
}

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
