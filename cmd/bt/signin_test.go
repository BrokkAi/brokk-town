package main

import (
	"strings"
	"testing"
)

const oneTown = `"towns":[{"repo":"acme/team","harness":"custom","agent":{"command":["fake"]},"merge_policy":"bot","poll_seconds":60,"report_seconds":60,"max_cycles":1}]`

func TestTheConfigFileTurnsOnSignIn(t *testing.T) {
	_, service, err := decodeConfigFile([]byte(`{"auth":{"github":{"client_id":"Iv1.abc","client_secret_file":"/run/secrets/github-oauth","organizations":["BrokkAi"]}},` + oneTown + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if service.Auth == nil || service.Auth.GitHub.ClientID != "Iv1.abc" || service.Auth.GitHub.Organizations[0] != "BrokkAi" {
		t.Fatalf("auth %+v", service.Auth)
	}
	_, service, err = decodeConfigFile([]byte(`{"auth":{"allow_insecure_http":true,"admins":["octocat"],"github":{"client_id":"x","client_secret_file":"/s","users":["a"],"url":"https://ghe.internal","api_url":"https://ghe.internal/api/v3","ca_file":"/etc/brokk/ghe-ca.pem"}},` + oneTown + `}`))
	if err != nil || !service.Auth.AllowInsecureHTTP || service.Auth.GitHub.CAFile != "/etc/brokk/ghe-ca.pem" || service.Auth.Admins[0] != "octocat" {
		t.Fatalf("an internal Town's auth %+v %v", service.Auth, err)
	}
	_, service, err = decodeConfigFile([]byte(`{` + oneTown + `}`))
	if err != nil || service.Auth != nil {
		t.Fatalf("a config without auth turned it on: %+v %v", service.Auth, err)
	}
	for raw, want := range map[string]string{
		`{"auth":null,` + oneTown + `}`: "auth must be an object",
		`{"auth":{"github":{"client_id":"x","client_secret":"inline","users":["a"]}},` + oneTown + `}`:                    "unknown field",
		`{"auth":{"github":{"client_id":"x","client_secret_file":"/s"}},` + oneTown + `}`:                                 "list the users or organizations",
		`{"auth":{"github":{"client_id":"x","client_secret_file":"/s","users":["a"],"url":"http://gh"}},` + oneTown + `}`: "https URL",
	} {
		if _, _, err := decodeConfigFile([]byte(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", raw, err, want)
		}
	}
}

func TestThePublicAddressMayBeHTTPS(t *testing.T) {
	for publicURL, want := range map[string]string{
		"http://127.0.0.1:8080":     "http://127.0.0.1:8080",
		"https://town.example":      "https://town.example",
		"https://town.example/":     "https://town.example",
		"https://town.example:8443": "https://town.example:8443",
		"https://town.example/path": "",
		"ftp://town.example":        "",
		"https://user@town.example": "",
		"https://town.example/?x=1": "",
	} {
		got, err := serviceOrigin("0.0.0.0:8080", publicURL)
		if want == "" {
			if err == nil {
				t.Errorf("%s was accepted as %s", publicURL, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%s: got %q %v, want %q", publicURL, got, err, want)
		}
	}
}
