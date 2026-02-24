package journal_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	"io"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openSUSE/systemd-mcp/authkeeper"
	"github.com/openSUSE/systemd-mcp/internal/pkg/journal"
	"github.com/openSUSE/systemd-mcp/internal/pkg/testframework"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
)

func runIntegrationJournalList(t *testing.T) {
	ctx := context.Background()

	auth, err := authkeeper.NewNoAuth()
	if err != nil {
		t.Fatalf("Failed to create authkeeper: %v", err)
	}

	hostLog, err := journal.NewLog(auth)
	if err != nil {
		t.Fatalf("Failed to create log connection: %v", err)
	}
	defer hostLog.Close()

	// Give the dummy service a second to generate a log entry
	time.Sleep(2 * time.Second)

	params := &journal.ListLogParams{
		Unit:  "dummy.service",
		Count: 10,
	}

	res, _, err := hostLog.ListLog(ctx, nil, params)
	assert.NoError(t, err)
	assert.NotNil(t, res)

	assert.True(t, len(res.Content) > 0)
	textContent, ok := res.Content[0].(*mcp.TextContent)
	assert.True(t, ok)

	var logRes journal.ListLogResult
	err = json.Unmarshal([]byte(textContent.Text), &logRes)
	assert.NoError(t, err)

	found := false
	for _, msg := range logRes.Messages {
		if strings.Contains(msg.Msg, "Hello from dummy service") {
			found = true
			break
		}
	}
	assert.True(t, found, "Expected to find dummy service log message")
}

func TestIntegrationJournal(t *testing.T) {
	if os.Getenv("IN_CONTAINER") == "1" {
		runIntegrationJournalList(t)
		return
	}

	testframework.SetupPodmanEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	testBinPath := "journal.test"
	err := testframework.BuildGoTestBinary(t, ".", testBinPath)
	if err != nil {
		t.Fatalf("Failed to build test binary: %v", err)
	}
	defer os.Remove(testBinPath)

	dummyService := `[Unit]
Description=Dummy Service

[Service]
ExecStart=/bin/sh -c 'echo "Hello from dummy service"; sleep 3600'

[Install]
WantedBy=multi-user.target
`

	files := []testcontainers.ContainerFile{
		{
			HostFilePath:      "./" + testBinPath,
			ContainerFilePath: "/tmp/journal.test",
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

	// Reload systemd daemon
	_, _, err = container.Exec(ctx, []string{"systemctl", "daemon-reload"})
	if err != nil {
		t.Fatalf("Failed to reload systemd daemon: %v", err)
	}

	// Start the dummy service
	_, _, err = container.Exec(ctx, []string{"systemctl", "start", "dummy.service"})
	if err != nil {
		t.Fatalf("Failed to start dummy service: %v", err)
	}

	// Run the test inside the container
	code, outReader, err := container.Exec(ctx, []string{"env", "IN_CONTAINER=1", "/tmp/journal.test", "-test.v", "-test.run", "TestIntegrationJournal"})
	
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