package authkeeper_test

import (
	"context"
	"testing"

	"github.com/openSUSE/systemd-mcp/authkeeper"
	"github.com/stretchr/testify/assert"
)

func TestNewNoAuth(t *testing.T) {
	auth, err := authkeeper.NewNoAuth(true, true)
	assert.NoError(t, err)
	assert.NotNil(t, auth)

	readAllowed, err := auth.IsReadAuthorized(context.Background())
	assert.NoError(t, err)
	assert.True(t, readAllowed)

	writeAllowed, err := auth.IsWriteAuthorized(context.Background())
	assert.NoError(t, err)
	assert.True(t, writeAllowed)
}

func TestDeauthorizeNoAuth(t *testing.T) {
	auth, err := authkeeper.NewNoAuth(true, true)
	assert.NoError(t, err)

	errDeauth := auth.Deauthorize()
	assert.Nil(t, errDeauth)
}
