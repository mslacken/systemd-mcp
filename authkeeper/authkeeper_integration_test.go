package authkeeper_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openSUSE/systemd-mcp/authkeeper"
	"github.com/openSUSE/systemd-mcp/internal/pkg/testframework"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
)

func runIntegrationPolkitAuthSetup(t *testing.T) {
	dbusName := "org.opensuse.systemdmcp.authkeeper"
	dbusPath := "/org/opensuse/systemdmcp/authkeeper"

	// Check if taken initially
	taken, err := authkeeper.IsDBusNameTaken(dbusName)
	assert.NoError(t, err)
	assert.False(t, taken, "DBus name should not be taken initially")

	auth, err := authkeeper.NewPolkitAuth(dbusName, dbusPath)
	assert.NoError(t, err)
	assert.NotNil(t, auth)

	// After setting up, the name should be taken
	taken, err = authkeeper.IsDBusNameTaken(dbusName)
	assert.NoError(t, err)
	assert.True(t, taken, "DBus name should be taken now")

	// Verify modes
	assert.Equal(t, uint(2), uint(auth.Mode())) // polkit is 2

	// IsReadAuthorized should fail predictably in this container context
	readAuth, errRead := auth.IsReadAuthorized(context.Background())
	assert.False(t, readAuth)
	assert.ErrorContains(t, errRead, "must be authorized externally")

	writeAuth, errWrite := auth.IsWriteAuthorized(context.Background(), "")
	assert.False(t, writeAuth)
	assert.ErrorContains(t, errWrite, "must be authorized externally")

	errDeauth := auth.Deauthorize()
	assert.Nil(t, errDeauth)

	errClose := auth.Close()
	assert.NoError(t, errClose)
}

func TestIntegrationAuthKeeper(t *testing.T) {
	if os.Getenv("IN_CONTAINER") == "1" {
		runIntegrationPolkitAuthSetup(t)
		return
	}

	testframework.SetupPodmanEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	testBinPath := "authkeeper.test"
	err := testframework.BuildGoTestBinary(t, ".", testBinPath)
	if err != nil {
		t.Fatalf("Failed to build test binary: %v", err)
	}
	defer os.Remove(testBinPath)

	dbusConf := `<!DOCTYPE busconfig PUBLIC
 "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <policy context="default">
    <allow send_destination="org.opensuse.systemdmcp.authkeeper"/>
    <allow own="org.opensuse.systemdmcp.authkeeper"/>
  </policy>
</busconfig>`

	files := []testcontainers.ContainerFile{
		{
			HostFilePath:      "./" + testBinPath,
			ContainerFilePath: "/tmp/" + testBinPath,
			FileMode:          0755,
		},
		{
			Reader:            strings.NewReader(dbusConf),
			ContainerFilePath: "/etc/dbus-1/system.d/org.opensuse.systemdmcp.authkeeper.conf",
			FileMode:          0644,
		},
	}

	container, err := testframework.StartSystemdContainer(ctx, t, files)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer container.Terminate(ctx)

	code, outReader, err := container.Exec(ctx, []string{"env", "IN_CONTAINER=1", "/tmp/" + testBinPath, "-test.v", "-test.run", "TestIntegrationAuthKeeper"})

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
