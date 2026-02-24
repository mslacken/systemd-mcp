package dbus_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/openSUSE/systemd-mcp/internal/pkg/testframework"
	"github.com/stretchr/testify/assert"
	"github.com/testcontainers/testcontainers-go"
)

func runIntegrationDBusSetup(t *testing.T) {
	dbusName := "org.opensuse.systemdmcp"
	dbusPath := "/org/opensuse/systemdmcp"

	// Ensure dbus name is not taken initially
	taken, err := dbus.IsDBusNameTaken(dbusName)
	assert.NoError(t, err)
	assert.False(t, taken, "DBus name should not be taken initially")

	auth, err := dbus.SetupDBus(dbusName, dbusPath)
	assert.NoError(t, err)
	assert.NotNil(t, auth)

	// After setting up, the name should be taken
	taken, err = dbus.IsDBusNameTaken(dbusName)
	assert.NoError(t, err)
	assert.True(t, taken, "DBus name should be taken now")

	// Verify local check works without panic (though without a valid session it may return false)
	_ = auth.IsLocal()

	// Deauthorize should run without crashing
	errDeauth := auth.Deauthorize()
	assert.Nil(t, errDeauth)

	// In this test container without logind session, IsLocal() returns false,
	// so IsReadAuthorized and IsWriteAuthorized should fail predictably.
	readAuth, errRead := auth.IsReadAuthorized()
	assert.False(t, readAuth)
	assert.ErrorContains(t, errRead, "must be authorized externally")

	writeAuth, errWrite := auth.IsWriteAuthorized("")
	assert.False(t, writeAuth)
	assert.ErrorContains(t, errWrite, "must be authorized externally")
}

func TestIntegrationDBus(t *testing.T) {
	if os.Getenv("IN_CONTAINER") == "1" {
		runIntegrationDBusSetup(t)
		return
	}

	testframework.SetupPodmanEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	testBinPath := "dbus.test"
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
    <allow send_destination="org.opensuse.systemdmcp"/>
    <allow own="org.opensuse.systemdmcp"/>
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
			ContainerFilePath: "/etc/dbus-1/system.d/org.opensuse.systemdmcp.conf",
			FileMode:          0644,
		},
	}

	container, err := testframework.StartSystemdContainer(ctx, t, files)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer container.Terminate(ctx)

	// Run the test inside the container
	code, outReader, err := container.Exec(ctx, []string{"env", "IN_CONTAINER=1", "/tmp/" + testBinPath, "-test.v", "-test.run", "TestIntegrationDBus"})

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
