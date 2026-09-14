package town

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const LatestPackageURL = "https://registry.npmjs.org/@brokkai%2fbrokk-town/latest"

type UpdateNotice struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Command string `json:"command"`
}

func versionParts(value string) ([3]int, bool) {
	var parts [3]int
	value = strings.TrimPrefix(value, "v")
	if strings.ContainsAny(value, "-+") {
		return parts, false
	}
	items := strings.Split(value, ".")
	if len(items) != len(parts) {
		return parts, false
	}
	for i, item := range items {
		n, err := strconv.Atoi(item)
		if err != nil || n < 0 {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

func NewerVersion(current, latest string) bool {
	a, okA := versionParts(current)
	b, okB := versionParts(latest)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if b[i] != a[i] {
			return b[i] > a[i]
		}
	}
	return false
}

func CheckUpdate(ctx context.Context, client *http.Client, current string) (*UpdateNotice, error) {
	if _, ok := versionParts(current); !ok {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestPackageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("npm registry returned %s", resp.Status)
	}
	var result struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		return nil, err
	}
	if !NewerVersion(current, result.Version) {
		return nil, nil
	}
	return &UpdateNotice{Current: strings.TrimPrefix(current, "v"), Latest: result.Version, Command: "npm install -g @brokkai/brokk-town@" + result.Version}, nil
}
