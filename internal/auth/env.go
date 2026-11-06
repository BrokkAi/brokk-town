package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Environment variables that configure sign-in. Client secrets are read from
// files only: Town passes its environment to the subprocesses it starts, and a
// secret in a variable would travel with it.
const (
	EnvGitHubClientID         = "BROKK_TOWN_GITHUB_CLIENT_ID"
	EnvGitHubClientSecretFile = "BROKK_TOWN_GITHUB_CLIENT_SECRET_FILE"
	EnvGoogleClientID         = "BROKK_TOWN_GOOGLE_CLIENT_ID"
	EnvGoogleClientSecretFile = "BROKK_TOWN_GOOGLE_CLIENT_SECRET_FILE"
	EnvAllowGitHubUsers       = "BROKK_TOWN_ALLOW_GITHUB_USERS"
	EnvAllowGitHubOrgs        = "BROKK_TOWN_ALLOW_GITHUB_ORGS"
	EnvAllowGoogleDomains     = "BROKK_TOWN_ALLOW_GOOGLE_DOMAINS"
	EnvAllowEmails            = "BROKK_TOWN_ALLOW_EMAILS"
)

// FromEnvironment builds a Config from the environment. getenv is os.Getenv
// outside tests.
func FromEnvironment(origin string, key []byte, getenv func(string) string) (Config, error) {
	cfg := Config{Origin: origin, Key: key, Allow: Allow{
		GitHubUsers:   list(getenv(EnvAllowGitHubUsers)),
		GitHubOrgs:    list(getenv(EnvAllowGitHubOrgs)),
		GoogleDomains: list(getenv(EnvAllowGoogleDomains)),
		Emails:        list(getenv(EnvAllowEmails)),
	}}
	var err error
	if cfg.GitHub, err = provider("GitHub", getenv(EnvGitHubClientID), getenv(EnvGitHubClientSecretFile), EnvGitHubClientSecretFile); err != nil {
		return Config{}, err
	}
	if cfg.Google, err = provider("Google", getenv(EnvGoogleClientID), getenv(EnvGoogleClientSecretFile), EnvGoogleClientSecretFile); err != nil {
		return Config{}, err
	}
	for _, name := range []string{"BROKK_TOWN_GITHUB_CLIENT_SECRET", "BROKK_TOWN_GOOGLE_CLIENT_SECRET"} {
		if getenv(name) != "" {
			return Config{}, fmt.Errorf("%s is not read; put the secret in a file and name it with %s_FILE", name, name)
		}
	}
	return cfg, nil
}

func provider(name, id, secretFile, secretVar string) (*Provider, error) {
	id = strings.TrimSpace(id)
	if id == "" && secretFile == "" {
		return nil, nil
	}
	if id == "" || secretFile == "" {
		return nil, fmt.Errorf("the %s OAuth client needs both a client ID and %s", name, secretVar)
	}
	b, err := os.ReadFile(secretFile)
	if err != nil {
		return nil, fmt.Errorf("read the %s client secret: %w", name, err)
	}
	secret := strings.TrimSpace(string(b))
	if secret == "" {
		return nil, errors.New("the " + name + " client secret file is empty")
	}
	return &Provider{ClientID: id, ClientSecret: secret}, nil
}

func list(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
}
