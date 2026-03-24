package authkeeper

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	godbus "github.com/godbus/dbus/v5"
	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/openSUSE/systemd-mcp/remoteauth"
)

type AuthKeeper struct {
	Dbus         *dbus.DbusAuth
	Oauth2       *remoteauth.Oauth2Auth
	Timeout      uint32
	ReadAllowed  bool
	WriteAllowed bool
	context      context.Context
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
func NewPolkitAuth(dbusName, dbusPath string) (*AuthKeeper, error) {
	return &AuthKeeper{}, nil
}

// no auth at all
func NewNoAuth() (*AuthKeeper, error) {
	a := new(AuthKeeper)
	a.ReadAllowed = true
	a.WriteAllowed = true
	return a, nil
}

// remote auth with oauth2
func NewOauth(controller string, skipVerify bool) (*AuthKeeper, error) {
	if !strings.HasPrefix(controller, "http") {
		controller = "http://" + controller
	}
	a := new(AuthKeeper)
	jwksURI, err := remoteauth.GetJwksURI(controller, skipVerify)
	if err != nil {
		return a, err
	}
	a.context = context.Background()

	override := keyfunc.Override{}
	if skipVerify {
		override.Client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			Timeout: 10 * time.Second,
		}
	}

	keyf, err := keyfunc.NewDefaultOverrideCtx(a.context, []string{jwksURI}, override)
	if err != nil {
		return a, err
	}
	a.Oauth2 = &remoteauth.Oauth2Auth{KeyFunc: keyf}
	a.Oauth2.JwksUri = jwksURI
	return a, nil
}

func (a *AuthKeeper) Close() error {
	if a.Dbus != nil && a.Dbus.Conn != nil {
		return a.Dbus.Conn.Close()
	}
	return nil
}

// Delegate methods to Dbus

func (a *AuthKeeper) IsReadAuthorized(ctx context.Context) (bool, error) {
	switch a.Mode() {
	case oauth2:
		return a.Oauth2.IsReadAuthorized(ctx)
	case polkit:
		return a.Dbus.IsReadAuthorized(ctx)
	default:
		return a.ReadAllowed, nil
	}
}

func (a *AuthKeeper) IsWriteAuthorized(ctx context.Context) (bool, error) {
	switch a.Mode() {
	case oauth2:
		return a.Oauth2.IsWriteAuthorized(ctx)
	case polkit:
		return a.Dbus.IsWriteAuthorized(ctx)
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
