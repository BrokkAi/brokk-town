package town

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// CheckBotVersions reads npm's stable tag for each fixed bot package. It never
// changes a pin; the Mayor must explicitly save a selected version.
func CheckBotVersions(ctx context.Context, client *http.Client) (map[Role]string, error) {
	versions := make(map[Role]string, len(AgentRoles))
	for _, role := range AgentRoles {
		endpoint := "https://registry.npmjs.org/" + url.PathEscape(workerPackageNames[role]) + "/latest"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("check %s bot: %w", role, err)
		}
		var body struct {
			Version string `json:"version"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
		status := resp.StatusCode
		resp.Body.Close()
		// The initial simplifier package may not be published yet. An absent
		// package has no stable offer; other registry failures stay visible.
		if role == Simplifier && status == http.StatusNotFound {
			continue
		}
		if status != http.StatusOK || err != nil || !workerVersionPattern.MatchString(body.Version) {
			return nil, fmt.Errorf("check %s bot: npm returned an invalid response", role)
		}
		versions[role] = body.Version
	}
	return versions, nil
}
