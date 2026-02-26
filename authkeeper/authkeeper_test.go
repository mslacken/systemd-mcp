package authkeeper_test

import (
	"testing"

	"github.com/openSUSE/systemd-mcp/authkeeper"
	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/openSUSE/systemd-mcp/remoteauth"
	"github.com/stretchr/testify/assert"
)

func TestNewNoAuth(t *testing.T) {
	auth, err := authkeeper.NewNoAuth()
	assert.NoError(t, err)
	assert.NotNil(t, auth)

	readAllowed, err := auth.IsReadAuthorized(context.Background())
	assert.NoError(t, err)
	assert.True(t, readAllowed)

	writeAllowed, err := auth.IsWriteAuthorized(context.Background(), "")
	assert.NoError(t, err)
	assert.True(t, writeAllowed)
}

func TestMode(t *testing.T) {
	// Test noauth
	noAuth, err := authkeeper.NewNoAuth()
	assert.NoError(t, err)
	// authkeeper doesn't export Mode constants, but Mode() returns AuthMode (uint).
	// We can't directly check the unexported constant, but we know noauth is 0.
	assert.Equal(t, uint(0), uint(noAuth.Mode()))

	// Test invalid state
	invalidAuth := &authkeeper.AuthKeeper{
		Dbus:   &dbus.DbusAuth{},
		Oauth2: &remoteauth.Oauth2Auth{},
	}
	assert.Equal(t, uint(0), uint(invalidAuth.Mode())) // Should fall back to noauth
}

func TestDeauthorizeNoAuth(t *testing.T) {
	auth, err := authkeeper.NewNoAuth()
	assert.NoError(t, err)

	errDeauth := auth.Deauthorize()
	assert.Nil(t, errDeauth)
}
