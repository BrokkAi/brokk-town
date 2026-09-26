package mjolnir

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

type Choice struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

// ProfileConfig is the daemon's warm discovery result. It describes selectors,
// not a pinned runtime or proof that a target can run the profile.
type ProfileConfig struct {
	Models  []Choice `json:"models"`
	Efforts []Choice `json:"efforts"`
}

func (c *Catalog) ProfileConfig(ctx context.Context, selection Selection, model string) (ProfileConfig, error) {
	var result ProfileConfig
	if err := selection.Validate(); err != nil || !selection.Managed() {
		return result, errors.New("select a Mjolnir target and profile before loading choices")
	}
	if len(model) > 4096 || strings.ContainsFunc(model, unicode.IsControl) {
		return result, errors.New("invalid model selection")
	}
	path := "/profiles/" + url.PathEscape(selection.Profile) + "/config"
	if model != "" {
		path += "?" + url.Values{"model": {model}}.Encode()
	}
	if err := c.get(ctx, path, "profile choices", &result); err != nil {
		return ProfileConfig{}, err
	}
	if result.Models == nil || result.Efforts == nil || !validChoices(result.Models) || !validChoices(result.Efforts) {
		return ProfileConfig{}, errors.New("Mjolnir returned invalid profile choices; check its API version.")
	}
	return result, nil
}

func validChoices(choices []Choice) bool {
	seen := map[string]bool{}
	for _, choice := range choices {
		if choice.Value == "" || strings.TrimSpace(choice.Value) != choice.Value || len(choice.Value) > 4096 || len(choice.Name) > 4096 || strings.ContainsFunc(choice.Value+choice.Name, unicode.IsControl) || seen[choice.Value] {
			return false
		}
		seen[choice.Value] = true
	}
	return true
}
