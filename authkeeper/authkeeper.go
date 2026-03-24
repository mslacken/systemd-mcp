package authkeeper

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	godbus "github.com/godbus/dbus/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/openSUSE/systemd-mcp/dbus"
	"github.com/openSUSE/systemd-mcp/remoteauth"
)

type AuthKeeper interface {
	IsReadAuthorized(ctx context.Context) (bool, error)
	IsWriteAuthorized(ctx context.Context) (bool, error)
	Deauthorize() *godbus.Error
	Close() error
}

type noAuth struct {
	readAllowed  bool
	writeAllowed bool
}

func (a *noAuth) IsReadAuthorized(ctx context.Context) (bool, error) {
	return a.readAllowed, nil
}

func (a *noAuth) IsWriteAuthorized(ctx context.Context) (bool, error) {
	return a.writeAllowed, nil
}

func (a *noAuth) Deauthorize() *godbus.Error {
	return nil
}

func (a *noAuth) Close() error {
	return nil
}

type polkitAuth struct {
	dbus *dbus.DbusAuth
}

func (a *polkitAuth) IsReadAuthorized(ctx context.Context) (bool, error) {
	return a.dbus.IsReadAuthorized(ctx)
}

func (a *polkitAuth) IsWriteAuthorized(ctx context.Context) (bool, error) {
	return a.dbus.IsWriteAuthorized(ctx)
}

func (a *polkitAuth) Deauthorize() *godbus.Error {
	return a.dbus.Deauthorize()
}

func (a *polkitAuth) Close() error {
	if a.dbus != nil && a.dbus.Conn != nil {
		return a.dbus.Conn.Close()
	}
	return nil
}

type OAuth2Provider interface {
	AuthKeeper
	VerifyJWT(ctx context.Context, tokenString string, r *http.Request) (*auth.TokenInfo, error)
	JwksUri() string
}

type oauth2Auth struct {
	oauth   *remoteauth.Oauth2Auth
	context context.Context
}

func (a *oauth2Auth) IsReadAuthorized(ctx context.Context) (bool, error) {
	return a.oauth.IsReadAuthorized(ctx)
}

func (a *oauth2Auth) IsWriteAuthorized(ctx context.Context) (bool, error) {
	return a.oauth.IsWriteAuthorized(ctx)
}

func (a *oauth2Auth) Deauthorize() *godbus.Error {
	return nil
}

func (a *oauth2Auth) Close() error {
	return nil
}

func (a *oauth2Auth) VerifyJWT(ctx context.Context, tokenString string, r *http.Request) (*auth.TokenInfo, error) {
	return a.oauth.VerifyJWT(ctx, tokenString, r)
}

func (a *oauth2Auth) JwksUri() string {
	return a.oauth.JwksUri
}

// setup the dbus authorization call back.
func NewPolkitAuth(dbusName, dbusPath string, timeout uint32) (AuthKeeper, error) {
	conn, err := godbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	return &polkitAuth{
		dbus: &dbus.DbusAuth{
			Conn:     conn,
			DbusName: dbusName,
			DbusPath: dbusPath,
			Timeout:  timeout,
		},
	}, nil
}

// no auth at all
func NewNoAuth(readAllowed, writeAllowed bool) (AuthKeeper, error) {
	return &noAuth{
		readAllowed:  readAllowed,
		writeAllowed: writeAllowed,
	}, nil
}

// remote auth with oauth2
func NewOauth(controller string, skipVerify bool) (AuthKeeper, error) {
	if !strings.HasPrefix(controller, "http") {
		controller = "http://" + controller
	}
	jwksURI, err := remoteauth.GetJwksURI(controller, skipVerify)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()

	override := keyfunc.Override{}
	if skipVerify {
		override.Client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			Timeout: 10 * time.Second,
		}
	}

	keyf, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{jwksURI}, override)
	if err != nil {
		return nil, err
	}
	return &oauth2Auth{
		oauth: &remoteauth.Oauth2Auth{
			KeyFunc: keyf,
			JwksUri: jwksURI,
		},
		context: ctx,
	}, nil
}
