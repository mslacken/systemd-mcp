package journal

import (
	"context"
	"fmt"
	"testing"

	"github.com/coreos/go-systemd/v22/sdjournal"
	godbus "github.com/godbus/dbus/v5"
	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateListLogsSchema(t *testing.T) {
	schema := CreateListLogsSchema()
	assert.NotNil(t, schema)
	assert.Contains(t, schema.Properties, "count")
	assert.Contains(t, schema.Properties, "offset")
	assert.Contains(t, schema.Properties, "unit")
}

// an AuthKeeper which always fails as polkit doesn't know the action
type unregisteredAuth struct{}

func (unregisteredAuth) IsReadAuthorized(ctx context.Context) (bool, error) {
	return false, fmt.Errorf("%w: %s", dbus.ErrActionNotRegistered, "com.suse.gatekeeper.readlog")
}

func (unregisteredAuth) IsWriteAuthorized(ctx context.Context) (bool, error) {
	return false, fmt.Errorf("%w: %s", dbus.ErrActionNotRegistered, "com.suse.gatekeeper.readlog")
}

func (unregisteredAuth) Deauthorize() *godbus.Error { return nil }

func (unregisteredAuth) Close() error { return nil }

// without the gatekeeper package polkit can't grant its action, which must not
// block the journal files the calling user may read anyway
func TestSelfInitWithoutPolkitPolicy(t *testing.T) {
	sj := &HostLog{journal: &sdjournal.Journal{}, Auth: unregisteredAuth{}}
	if sj.isJournalGroupMember() {
		t.Skip("running as a member of the journal group, no authorization needed")
	}

	sj.access = accessUser
	allowed, err := sj.self_init(context.Background())
	require.NoError(t, err)
	assert.True(t, allowed)

	// with the whole journal open the missing policy is a real error
	sj.access = accessFull
	allowed, err = sj.self_init(context.Background())
	assert.ErrorIs(t, err, dbus.ErrActionNotRegistered)
	assert.False(t, allowed)
}
