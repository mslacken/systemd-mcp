package remoteauth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	DefaultProtectedResourceMetadataURI = "/.well-known/oauth-protected-resource"
)

var (
	Audience        = "echo-mcp-server"
	ScopesSupported = []string{"mcp:read", "mcp:tools"} // mcp-user
)

type Verifier struct {
	KeyFunc keyfunc.Keyfunc
}

func (v Verifier) VerifyJWT(_ context.Context, tokenString string, _ *http.Request) (*auth.TokenInfo, error) {
	log.Printf("verifier received token: %s", tokenString)

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, &claims, v.KeyFunc.Keyfunc, jwt.WithAudience(Audience),
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Name}))
	if err != nil {
		// Uncomment panic to stop mcp inspector spinning sometimes - it's tedious to kill/restart.
		// Rate limiting middleware is needed to protect against buggy/misbehaving clients.
		// See go-sdk examples/server/rate-limiting/.
		// log.Panicf("err: %v", err)
		return nil, fmt.Errorf("%v: %w", auth.ErrInvalidToken, err)
	}
	for k, v := range claims {
		log.Printf("claim: %v: %v", k, v)
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
		return &auth.TokenInfo{
			Scopes:     strings.Split(scopes, " "),
			Expiration: expireTime.Time,
		}, nil
	}
	return nil, auth.ErrInvalidToken
}

// getJwksUri gets the jwks_uri from the OpenID Provider configuration information.
// See https://openid.net/specs/openid-connect-discovery-1_0.html
func GetJwksURI(issuer string) (string, error) {
	resp, err := http.Get(issuer + "/.well-known/openid-configuration")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	openIDConfig := struct {
		JwksURI string `json:"jwks_uri"`
	}{}

	err = json.NewDecoder(resp.Body).Decode(&openIDConfig)
	if err != nil {
		return "", err
	}

	return openIDConfig.JwksURI, nil
}
