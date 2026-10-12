package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

// GoogleIdentity is the subset of Google's userinfo response we use.
type GoogleIdentity struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

type Google struct {
	conf *oauth2.Config
}

func NewGoogle(clientID, clientSecret, redirectURL string) *Google {
	return &Google{conf: &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{"openid", "email", "profile"},
		Endpoint:     google.Endpoint,
	}}
}

// AuthURL returns the consent-screen URL. verifier is the PKCE code verifier.
func (g *Google) AuthURL(state, verifier string) string {
	return g.conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

// Exchange trades an authorization code for the user's verified identity.
func (g *Google) Exchange(ctx context.Context, code, verifier string) (GoogleIdentity, error) {
	var id GoogleIdentity
	tok, err := g.conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return id, fmt.Errorf("exchange code: %w", err)
	}
	resp, err := g.conf.Client(ctx, tok).Get(googleUserInfoURL)
	if err != nil {
		return id, fmt.Errorf("fetch userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return id, fmt.Errorf("fetch userinfo: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		return id, fmt.Errorf("decode userinfo: %w", err)
	}
	if id.Subject == "" || id.Email == "" {
		return id, errors.New("userinfo missing subject or email")
	}
	if !id.EmailVerified {
		return id, errors.New("google email is not verified")
	}
	return id, nil
}
