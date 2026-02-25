package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openSUSE/systemd-mcp/internal/pkg/testframework"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
)

func TestSystemdMCPWithPodman(t *testing.T) {
	testframework.SetupPodmanEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Build the binary for Linux
	err := testframework.BuildGoBinary(t, ".", "systemd-mcp-linux")
	if err != nil {
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

	files := []testcontainers.ContainerFile{
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
	}

	container, err := testframework.StartSystemdContainer(ctx, t, files)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		if err := container.Terminate(ctx); err != nil {
			t.Fatalf("Failed to terminate container: %v", err)
		}
	}()

	err = testframework.WaitForService(ctx, t, container, "systemd-mcp")
	if err != nil {
		t.Fatalf("Failed to start systemd-mcp service: %v", err)
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

func TestSystemdMCPWithKeycloak(t *testing.T) {
	testframework.SetupPodmanEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second) // Give more time for Keycloak
	defer cancel()

	// 1. Build the binary for Linux
	err := testframework.BuildGoBinary(t, ".", "systemd-mcp-keycloak-linux")
	if err != nil {
		t.Fatalf("Failed to build systemd-mcp: %v", err)
	}
	defer os.Remove("systemd-mcp-keycloak-linux")

	// 2. Start Keycloak container
	absPath, err := filepath.Abs("config.json")
	if err != nil {
		t.Fatalf("Failed to get abs path of config.json: %v", err)
	}
	keycloakC, err := testframework.StartKeycloakContainer(ctx, t, absPath)
	if err != nil {
		t.Fatalf("Failed to start keycloak: %v", err)
	}
	defer keycloakC.Terminate(ctx)

	kcIP, err := keycloakC.ContainerIP(ctx)
	if err != nil {
		t.Fatalf("Failed to get Keycloak IP: %v", err)
	}
	controllerURL := fmt.Sprintf("http://%s:8080/realms/mcp-realm", kcIP)

	// Create a simple systemd service file to run our server in the container
	serviceFileContent := fmt.Sprintf(`[Unit]
Description=Systemd MCP Server
After=network.target

[Service]
ExecStart=/usr/local/bin/systemd-mcp --http :8080 --controller %s --log-json
Restart=always

[Install]
WantedBy=multi-user.target
`, controllerURL)

	files := []testcontainers.ContainerFile{
		{
			HostFilePath:      "./systemd-mcp-keycloak-linux",
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
	}

	container, err := testframework.StartSystemdContainer(ctx, t, files)
	if err != nil {
		t.Fatalf("Failed to start systemd container: %v", err)
	}
	defer container.Terminate(ctx)

	err = testframework.WaitForService(ctx, t, container, "systemd-mcp")
	if err != nil {
		_, outReader, _ := container.Exec(ctx, []string{"journalctl", "-u", "systemd-mcp", "--no-pager"})
		var outStr string
		if outReader != nil {
			if b, err := io.ReadAll(outReader); err == nil {
				outStr = string(b)
			}
		}
		t.Fatalf("Failed to start systemd-mcp service: %v\nLogs: %s", err, outStr)
	}

	time.Sleep(5 * time.Second)

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

	// 4. Test HTTP endpoint directly without token (should fail)
	client := http.Client{
		Timeout: 10 * time.Second,
	}
	reqHTTP, err := http.NewRequest("GET", baseURL+"/mcp", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	resp, err := client.Do(reqHTTP)
	if err != nil {
		t.Fatalf("Failed to connect to endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected unauthorized/forbidden status, got: %d", resp.StatusCode)
	}

	// 5. Get token from Keycloak
	kcHostIP, err := keycloakC.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kcPort, err := keycloakC.MappedPort(ctx, "8080")
	if err != nil {
		t.Fatal(err)
	}
	tokenURL := fmt.Sprintf("http://%s:%s/realms/mcp-realm/protocol/openid-connect/token", kcHostIP, kcPort.Port())

	form := url.Values{}
	form.Add("client_id", "systemd-mcp")
	form.Add("username", "mcp-user")
	form.Add("password", "user123")
	form.Add("grant_type", "password")
	form.Add("scope", "openid systemd-audience mcp:read")

	reqToken, err := http.NewRequest("POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("Failed to create token request: %v", err)
	}
	reqToken.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	respToken, err := client.Do(reqToken)
	if err != nil {
		t.Fatalf("Failed to get token: %v", err)
	}
	defer respToken.Body.Close()

	if respToken.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respToken.Body)
		t.Fatalf("Failed to get token, status: %d, body: %s", respToken.StatusCode, string(b))
	}

	var tokenResponse struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(respToken.Body).Decode(&tokenResponse); err != nil {
		t.Fatalf("Failed to decode token response: %v", err)
	}

	// 6. Test HTTP endpoint with token
	reqHTTPWithToken, err := http.NewRequest("POST", baseURL+"/mcp", strings.NewReader(`{"jsonrpc": "2.0", "id": 1, "method": "ping"}`))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	// For the SDK's streamable handler, we need the right Accept header to initiate SSE or it acts as the message endpoint
	reqHTTPWithToken.Header.Set("Accept", "application/json, text/event-stream")
	reqHTTPWithToken.Header.Set("Authorization", "Bearer "+tokenResponse.AccessToken)
	respWithToken, err := client.Do(reqHTTPWithToken)
	if err != nil {
		t.Fatalf("Failed to connect to endpoint with token: %v", err)
	}
	defer respWithToken.Body.Close()

	if respWithToken.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respWithToken.Body)
		t.Logf("Response body: %s", string(b))
	}

	assert.Equal(t, http.StatusOK, respWithToken.StatusCode)
}
