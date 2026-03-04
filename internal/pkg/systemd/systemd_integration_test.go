package systemd_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/authkeeper"
	"github.com/openSUSE/systemd-mcp/internal/pkg/systemd"
	"github.com/openSUSE/systemd-mcp/internal/pkg/testframework"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
)

func runIntegrationChangeUnitState(t *testing.T) {
	ctx := context.Background()

	auth, err := authkeeper.NewNoAuth()
	if err != nil {
		t.Fatalf("Failed to create authkeeper: %v", err)
	}

	conn, err := systemd.NewSystem(ctx, auth)
	if err != nil {
		t.Fatalf("Failed to create systemd connection: %v", err)
	}
	defer conn.Close()

	// Wait a bit for dbus and systemd to settle in the container
	time.Sleep(2 * time.Second)

	params := &systemd.ChangeUnitStateParams{
		Name:    "dummy.service",
		Action:  "start",
		Mode:    "replace",
		TimeOut: 30,
	}

	res, _, err := conn.ChangeUnitState(ctx, nil, params)
	assert.NoError(t, err)
	assert.NotNil(t, res)

	// Now try restart
	paramsRestart := &systemd.ChangeUnitStateParams{
		Name:    "dummy.service",
		Action:  "restart",
		Mode:    "replace",
		TimeOut: 30,
	}
	
	resRestart, _, err := conn.ChangeUnitState(ctx, nil, paramsRestart)
	assert.NoError(t, err)
	assert.NotNil(t, resRestart)

	// Verify the state via ListUnits
	listParams := &systemd.ListUnitsParams{
		Patterns: []string{"dummy.service"},
	}
	listRes, _, err := conn.ListUnits(ctx, nil, listParams)
	assert.NoError(t, err)
	assert.NotNil(t, listRes)
	
	// Check the content indicates it's running
	found := false
	for _, content := range listRes.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			if strings.Contains(textContent.Text, "dummy.service") && strings.Contains(textContent.Text, "active") {
				found = true
				break
			}
		}
	}
	assert.True(t, found, "dummy.service should be active")
}

func TestIntegrationChangeUnitState(t *testing.T) {
	if os.Getenv("IN_CONTAINER") == "1" {
		runIntegrationChangeUnitState(t)
		return
	}

	// We are on the host. Build the test binary and run it inside the container.
	testframework.SetupPodmanEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	testBinPath := "systemd.test"
	err := testframework.BuildGoTestBinary(t, ".", testBinPath)
	if err != nil {
		t.Fatalf("Failed to build test binary: %v", err)
	}
	defer os.Remove(testBinPath)

	dummyService := `[Unit]
Description=Dummy Service

[Service]
ExecStart=/bin/sleep 3600

[Install]
WantedBy=multi-user.target
`

	files := []testcontainers.ContainerFile{
		{
			HostFilePath:      "./" + testBinPath,
			ContainerFilePath: "/tmp/systemd.test",
			FileMode:          0755,
		},
		{
			Reader:            strings.NewReader(dummyService),
			ContainerFilePath: "/etc/systemd/system/dummy.service",
			FileMode:          0644,
		},
	}

	container, err := testframework.StartSystemdContainer(ctx, t, files)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer container.Terminate(ctx)

	// Reload systemd daemon to pick up the new unit file
	_, _, err = container.Exec(ctx, []string{"systemctl", "daemon-reload"})
	if err != nil {
		t.Fatalf("Failed to reload systemd daemon: %v", err)
	}

	// Run the test inside the container
	code, outReader, err := container.Exec(ctx, []string{"env", "IN_CONTAINER=1", "/tmp/systemd.test", "-test.v", "-test.run", "TestIntegrationChangeUnitState"})
	
	var outStr string
	if outReader != nil {
		if b, readErr := io.ReadAll(outReader); readErr == nil {
			outStr = string(b)
		}
	}

	if err != nil {
		t.Fatalf("Exec failed: %v\nOutput: %s", err, outStr)
	}

	if code != 0 {
		t.Fatalf("Test failed inside container (exit code %d).\nOutput:\n%s", code, outStr)
	} else {
		t.Logf("Test succeeded inside container.\nOutput:\n%s", outStr)
	}
}
