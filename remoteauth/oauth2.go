package remoteauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	DefaultProtectedResourceMetadataURI = "/.well-known/oauth-protected-resource"
)

var (
	Audience        = "systemd-mcp-server"
	ScopesSupported = []string{"mcp:read", "mcp:write"} // mcp-user
)

type Oauth2Auth struct {
	KeyFunc keyfunc.Keyfunc // Check oauth2 token func
	JwksUri string
	claims  jwt.MapClaims
}

func NewOutah2Auth() Oauth2Auth {
	a := Oauth2Auth{
		claims: make(jwt.MapClaims),
	}
	return a
}

// getJwksUri gets the jwks_uri from the OpenID Provider configuration information.
// See https://openid.net/specs/openid-connect-discovery-1_0.html
func GetJwksURI(issuer string) (string, error) {
	resp, err := http.Get(issuer + "/.well-known/openid-configuration")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("failed to get openid-configuration", "status", resp.Status, "url", issuer+"/.well-known/openid-configuration")
		return "", fmt.Errorf("failed to get openid-configuration: %s", resp.Status)
	}

	openIDConfig := struct {
		JwksURI string `json:"jwks_uri"`
	}{}

	err = json.NewDecoder(resp.Body).Decode(&openIDConfig)
	if err != nil {
		return "", err
	}

	return openIDConfig.JwksURI, nil
}

func (a *Oauth2Auth) VerifyJWT(ctx context.Context, tokenString string, _ *http.Request) (*auth.TokenInfo, error) {
	slog.Debug("verifier received token", "value", tokenString)
	claims := make(jwt.MapClaims)
	token, err := jwt.ParseWithClaims(tokenString, claims, a.KeyFunc.Keyfunc, jwt.WithAudience(Audience),
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Name}))
	if err != nil {
		slog.Debug("couldn't parse token", "error", err)
		return nil, fmt.Errorf("%v: %w", auth.ErrInvalidToken, err)
	}
	if token.Valid {
		expireTime, err := claims.GetExpirationTime()
		if err != nil {
			return nil, fmt.Errorf("%v: %w", auth.ErrInvalidToken, err)
		}
		scopes, ok := claims["scope"].(string)
		if !ok {
			return nil, fmt.Errorf("unable to type assert scopes: %w", auth.ErrInvalidToken)
		}
		
		var roles []string
		if realmAccess, ok := claims["realm_access"].(map[string]any); ok {
			if r, ok := realmAccess["roles"].([]any); ok {
				for _, role := range r {
					if roleStr, ok := role.(string); ok {
						roles = append(roles, roleStr)
					}
				}
			}
		}

		slog.Debug("scopes", "slice", strings.Split(scopes, " "), "roles", roles)
		return &auth.TokenInfo{
			Scopes:     strings.Split(scopes, " "),
			Expiration: expireTime.Time,
			Extra: map[string]any{
				"roles": roles,
			},
		}, nil
	}
	return nil, auth.ErrInvalidToken
}

// check if write is authorized via mcp:write and mcp-admin role
func (a *Oauth2Auth) IsWriteAuthorized(ctx context.Context) (bool, error) {
	ti := auth.TokenInfoFromContext(ctx)
	if ti == nil {
		slog.Debug("IsWriteAuthorized: NO TOKEN INFO")
		return false, fmt.Errorf("no token info in context")
	}
	
	hasWriteScope := slices.Contains(ti.Scopes, "mcp:write")
	hasAdminRole := false
	if rolesRaw, ok := ti.Extra["roles"]; ok {
		if roles, ok := rolesRaw.([]string); ok {
			hasAdminRole = slices.Contains(roles, "mcp-admin")
		}
	}

	slog.Debug("IsWriteAuthorized", "scopes", ti.Scopes, "hasAdminRole", hasAdminRole)
	if hasWriteScope && hasAdminRole {
		return true, nil
	}
	return false, fmt.Errorf("write unauthorized (mcp:write=%v, mcp-admin=%v)", hasWriteScope, hasAdminRole)
}

// check if read is authorized via mcp:read
func (a *Oauth2Auth) IsReadAuthorized(ctx context.Context) (bool, error) {
	ti := auth.TokenInfoFromContext(ctx)
	if ti == nil {
		return false, fmt.Errorf("no token info in context")
	}
	if slices.Contains(ti.Scopes, "mcp:read") {
		return true, nil
	}
	return false, fmt.Errorf("mcp:read not in scopes: %v", ti.Scopes)
}
