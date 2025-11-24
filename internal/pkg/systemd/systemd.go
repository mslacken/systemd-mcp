package systemd

import (
	"context"

	"github.com/coreos/go-systemd/v22/dbus"
	auth "github.com/openSUSE/systemd-mcp/dbus"
)

// DbusConnection is an interface that abstracts the dbus connection.
// This is primarily for testing purposes.
type DbusConnection interface {
	ListUnitsByPatternsContext(ctx context.Context, states []string, patterns []string) ([]dbus.UnitStatus, error)
	GetAllPropertiesContext(ctx context.Context, unitName string) (map[string]interface{}, error)
	ReloadOrRestartUnitContext(ctx context.Context, name string, mode string, ch chan<- string) (int, error)
	RestartUnitContext(ctx context.Context, name string, mode string, ch chan<- string) (int, error)
	StartUnitContext(ctx context.Context, name string, mode string, ch chan<- string) (int, error)
	StopUnitContext(ctx context.Context, name string, mode string, ch chan<- string) (int, error)
	KillUnitContext(ctx context.Context, name string, signal int32)
	EnableUnitFilesContext(ctx context.Context, files []string, runtime bool, force bool) (bool, []dbus.EnableUnitFileChange, error)
	DisableUnitFilesContext(ctx context.Context, files []string, runtime bool) ([]dbus.DisableUnitFileChange, error)
	ListUnitFilesContext(ctx context.Context) ([]dbus.UnitFile, error)

	Close()
}

type Connection struct {
	rchannel chan string
	dbus     DbusConnection
	auth     *auth.AuthKeeper
}

// opens a new user connection to the dbus
func NewUser(ctx context.Context) (conn *Connection, err error) {
	conn = new(Connection)
	conn.dbus, err = dbus.NewUserConnectionContext(ctx)
	if err != nil {
		return nil, err
	}
	return conn, err
}
func NewSystem(ctx context.Context, auth *auth.AuthKeeper) (conn *Connection, err error) {
	conn = new(Connection)
	conn.auth = auth
	conn.dbus, err = dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return nil, err
	}
	return conn, err
}

// close the connection
func (conn *Connection) Close() {
	conn.dbus.Close()
	close(conn.rchannel)
}
