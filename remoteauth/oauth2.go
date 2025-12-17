package remoteauth

import (
	"encoding/json"
	"net/http"
)

const (
	DefaultProtectedResourceMetadataURI = "/.well-known/oauth-protected-resource"
)

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
