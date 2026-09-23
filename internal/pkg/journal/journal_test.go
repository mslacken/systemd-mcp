package journal

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/coreos/go-systemd/v22/sdjournal"
	godbus "github.com/godbus/dbus/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

func TestListLogForTimeRangeWithoutUnit(t *testing.T) {
	now := time.Now()
	from := now.Add(-10 * time.Minute)
	to := now.Add(5 * time.Minute)

	sj := &HostLog{
		Auth: unregisteredAuth{},
	}

	res, _, err := sj.ListLog(context.Background(), nil, &ListLogParams{
		Count:    10,
		From:     from,
		To:       to,
		AllBoots: true,
	})
	require.NoError(t, err, "getting logs for a time range without a unit must not fail")
	require.NotNil(t, res)

	textContent, ok := res.Content[0].(*mcp.TextContent)
	require.Truef(t, ok, "expected a text content, got %T", res.Content[0])

	var result ListLogResult
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &result))
	require.NotEmptyf(t, result.Messages, "expected log entries in [%s .. %s]", from, to)
	assert.LessOrEqual(t, len(result.Messages), 10)
	for i := range result.Messages {
		m := &result.Messages[i]
		assert.Falsef(t, m.Time.Before(from), "entry %d at %s is before the from time %s", i, m.Time, from)
		assert.Truef(t, m.Time.Before(to), "entry %d at %s is after the to time %s", i, m.Time, to)
	}
}

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
