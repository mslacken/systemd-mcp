package authkeeper

import (
	"context"
	"log/slog"
	"net/http"

	godbus "github.com/godbus/dbus/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/openSUSE/systemd-mcp/remoteauth"
)

// IsDBusNameTaken checks if the dbus name is already taken.
func IsDBusNameTaken(dbusName string) (bool, error) {
	return dbus.IsDBusNameTaken(dbusName)
}

type AuthKeeper struct {
	Dbus         *dbus.DbusAuth
	Oauth2       *remoteauth.Oauth2Auth
	Timeout      uint32
	ReadAllowed  bool
	WriteAllowed bool
}

func (a *AuthKeeper) Mode() AuthMode {
	// this shouldn't happen
	if a.Dbus != nil && a.Oauth2 != nil {
		slog.Warn("ouath2 and dbus/polkit authentication defined", "auth", "noauth")
		return noauth
	}
	if a.Dbus != nil {
		return polkit
	}
	if a.Oauth2 != nil {
		return oauth2
	}
	return noauth
}

type AuthMode uint

const (
	noauth AuthMode = iota
	oauth2
	polkit
)

// setup the dbus authorization call back.
func SetupDBus(dbusName, dbusPath string) (*AuthKeeper, error) {
	d, err := dbus.SetupDBus(dbusName, dbusPath)
	if err != nil {
		return nil, err
	}
	return &AuthKeeper{
		Dbus:   d,
		Oauth2: &remoteauth.Oauth2Auth{},
	}, nil
}

func (a *AuthKeeper) Close() error {
	if a.Dbus != nil && a.Dbus.Conn != nil {
		return a.Dbus.Conn.Close()
	}
	return nil
}

// Delegate methods to Dbus

func (a *AuthKeeper) IsReadAuthorized() (bool, error) {
	switch a.Mode() {
	case oauth2:
		return true, nil
	case polkit:
		return a.Dbus.IsReadAuthorized()
	default:
		return a.ReadAllowed, nil
	}
}

func (a *AuthKeeper) IsWriteAuthorized(systemdPermission string) (bool, error) {
	switch a.Mode() {
	case oauth2:
		return true, nil
	case polkit:
		return a.Dbus.IsWriteAuthorized("")
	default:
		return a.WriteAllowed, nil
	}
}

func (a *AuthKeeper) Deauthorize() *godbus.Error {
	if a.Dbus == nil {
		return nil
	}
	return a.Dbus.Deauthorize()
}

// Delegate methods to Oauth2

func (a *AuthKeeper) VerifyJWT(ctx context.Context, tokenString string, req *http.Request) (*auth.TokenInfo, error) {
	if a.Oauth2 == nil {
		a.Oauth2 = &remoteauth.Oauth2Auth{}
	}
	return a.Oauth2.VerifyJWT(ctx, tokenString, req)
}
