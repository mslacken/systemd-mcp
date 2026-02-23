package main_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
)

func TestSystemdMCPWithPodman(t *testing.T) {
	// Ensure podman socket is running
	if err := exec.Command("systemctl", "--user", "start", "podman.socket").Run(); err != nil {
		t.Logf("Failed to start podman.socket: %v (ignoring, as it might already be running or we might be in a different environment)", err)
	}

	// Set required environment variables for testcontainers to use podman
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	if os.Getenv("DOCKER_HOST") == "" {
		os.Setenv("DOCKER_HOST", fmt.Sprintf("unix:///run/user/%d/podman/podman.sock", os.Getuid()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Build the binary for Linux
	cmd := exec.Command("go", "build", "-o", "systemd-mcp-linux", ".")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build systemd-mcp: %v", err)
	}
	defer os.Remove("systemd-mcp-linux")

	// Create a simple systemd service file to run our server in the container
	serviceFileContent := `[Unit]
Description=Systemd MCP Server
After=network.target

[Service]
ExecStart=/usr/local/bin/systemd-mcp --http :8080 --noauth --log-json
Restart=always

[Install]
WantedBy=multi-user.target
`

	// 2. Define the container using openSUSE BCI init which has systemd as PID 1
	req := testcontainers.ContainerRequest{
		Image:        "registry.opensuse.org/opensuse/bci/bci-init",
		Privileged:   true, // Required for systemd
		ExposedPorts: []string{"8080/tcp"},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      "./systemd-mcp-linux",
				ContainerFilePath: "/usr/local/bin/systemd-mcp",
				FileMode:          0755,
			},
			{
				Reader:            strings.NewReader(serviceFileContent),
				ContainerFilePath: "/etc/systemd/system/systemd-mcp.service",
				FileMode:          0644,
			},
			{
				HostFilePath:      "./configs/org.opensuse.systemdmcp.conf",
				ContainerFilePath: "/etc/dbus-1/system.d/org.opensuse.systemdmcp.conf",
				FileMode:          0644,
			},
			{
				HostFilePath:      "./configs/org.opensuse.systemdmcp.policy",
				ContainerFilePath: "/usr/share/polkit-1/actions/org.opensuse.systemdmcp.policy",
				FileMode:          0644,
			},
		},
		// WaitingFor: wait.ForHTTP("/sse").WithPort("8080/tcp").WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
		ProviderType:     testcontainers.ProviderPodman,
	})
	if err != nil {
		t.Fatalf("Failed to start container: %s", err)
	}
	defer func() {
		if err := container.Terminate(ctx); err != nil {
			t.Fatalf("Failed to terminate container: %s", err)
		}
	}()

	// Wait for systemd to initialize and start the service
	var startErr error
	for i := 0; i < 10; i++ {
		time.Sleep(1 * time.Second)
		_, _, err := container.Exec(ctx, []string{"systemctl", "daemon-reload"})
		if err != nil {
			continue
		}
		_, outReader, err := container.Exec(ctx, []string{"systemctl", "enable", "--now", "systemd-mcp"})
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
		t.Fatalf("Failed to start systemd-mcp service: %v", startErr)
	}

	time.Sleep(5 * time.Second)
	code, outReader, err := container.Exec(ctx, []string{"journalctl", "-u", "systemd-mcp", "--no-pager"})
	var outStr string
	if outReader != nil {
		if b, err := io.ReadAll(outReader); err == nil {
			outStr = string(b)
		}
	}
	t.Logf("journalctl code=%d err=%v out:\n%s", code, err, outStr)

	code, outReader, err = container.Exec(ctx, []string{"systemctl", "status", "systemd-mcp"})
	var outStr2 string
	if outReader != nil {
		if b, err := io.ReadAll(outReader); err == nil {
			outStr2 = string(b)
		}
	}
	t.Logf("systemctl status code=%d err=%v out:\n%s", code, err, outStr2)

	// 3. Get container endpoint
	ip, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := fmt.Sprintf("http://%s:%s", ip, port.Port())

	// 4. Test HTTP endpoint directly to verify the server is running and returning SSE
	client := http.Client{
		Timeout: 10 * time.Second,
	}
	payload := `{"jsonrpc": "2.0", "id": 1, "method": "ping"}`
	reqHTTP, err := http.NewRequest("POST", baseURL+"/sse", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	reqHTTP.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(reqHTTP)
	if err != nil {
		t.Fatalf("Failed to connect to SSE endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Logf("Response body: %s", string(b))
	}

	// The MCP SSE endpoint should return 200 OK
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}
