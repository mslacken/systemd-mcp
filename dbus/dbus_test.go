package dbus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetExecutableName(t *testing.T) {
	exeName := getExecutableName()
	assert.NotEmpty(t, exeName, "Executable name should not be empty")

	// We can verify that it matches the base name of os.Executable()
	exePath, err := os.Executable()
	if err == nil {
		assert.Equal(t, filepath.Base(exePath), exeName)
	}
}

func TestGetSessionIdFromPid_Self(t *testing.T) {
	// Let's try to get session ID for our own PID.
	// In some environments like containers, this might fail with "session scope not found",
	// but it shouldn't crash.
	pid := uint32(os.Getpid())
	sessionID, err := getSessionIdFromPid(pid)

	if err != nil {
		// If it errors, we expect it to be the specific format error or file open error.
		// It usually fails inside standard docker/podman without systemd.
		if !strings.Contains(err.Error(), "session scope not found") && !os.IsNotExist(err) {
			t.Errorf("Unexpected error: %v", err)
		}
	} else {
		// If it succeeds, sessionID should not be empty
		assert.NotEmpty(t, sessionID)
	}
}

func TestGetSessionIdFromPid_Invalid(t *testing.T) {
	// Try a PID that definitely doesn't exist to trigger an error
	_, err := getSessionIdFromPid(9999999)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err) || strings.Contains(err.Error(), "no such file or directory"), "Expected file not found error")
}
