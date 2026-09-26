package mjolnir

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Placement struct {
	Workspace  Workspace `json:"workspace"`
	Bundle     string    `json:"bundle_id"`
	Repository string    `json:"repository_id"`
}

func (p Placement) Validate() error {
	if !ValidID(p.Workspace.ID) || !ValidID(p.Workspace.Name) || len(p.Workspace.Name) > 64 || !ValidID(p.Bundle) || !ValidID(p.Repository) {
		return errors.New("Mjolnir placement requires a saved workspace, bundle and repository")
	}
	return nil
}

// ResolvePlacement uses configured bundles, never quick bundles from a local
// worktree. Each Town repository reuses one named workspace. Artifact APIs
// currently address the primary repository; refuse ambiguous/multi-repository
// mappings until all sibling evidence can be verified too.
func (c *Catalog) ResolvePlacement(ctx context.Context, repo, workspace, bundle string) (Placement, error) {
	if !ValidID(repo) || len(strings.Split(repo, "/")) != 2 || !ValidID(workspace) || len(workspace) > 64 || (bundle != "" && !ValidID(bundle)) {
		return Placement{}, errors.New("invalid Mjolnir repository or workspace selection")
	}
	options, err := c.fetch(ctx)
	if err != nil {
		return Placement{}, err
	}
	var matches []Bundle
	for _, candidate := range options.Bundles {
		if bundle != "" && candidate.ID != bundle {
			continue
		}
		if len(candidate.Repositories) != 1 {
			continue
		}
		r := candidate.Repositories[0]
		if candidate.PrimaryRepository == r.ID && strings.EqualFold(r.GitHub, repo) && ValidID(candidate.ID) && ValidID(r.ID) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return Placement{}, errors.New("select one configured Mjolnir bundle whose sole primary repository matches this Town repository; ambiguous or sibling repositories cannot be dispatched")
	}
	var listing struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	if err := c.get(ctx, "/workspaces", "workspaces", &listing); err != nil {
		return Placement{}, err
	}
	var found []Workspace
	for _, candidate := range listing.Workspaces {
		if strings.EqualFold(candidate.Name, workspace) {
			found = append(found, candidate)
		}
	}
	if listing.Workspaces == nil || len(found) > 1 {
		return Placement{}, errors.New("Mjolnir returned missing or ambiguous workspace evidence")
	}
	if len(found) == 0 {
		// This API explicitly deduplicates by case-insensitive name. A later
		// dispatch can safely look up the same name after an interrupted create.
		data, _ := json.Marshal(map[string]string{"name": workspace})
		var created struct {
			Workspace Workspace `json:"workspace"`
		}
		if err := c.mutation(ctx, "/workspaces", data, &created, 201, 200); err != nil {
			return Placement{}, err
		}
		if !strings.EqualFold(created.Workspace.Name, workspace) {
			return Placement{}, errors.New("Mjolnir returned a different workspace")
		}
		found = append(found, created.Workspace)
	}
	p := Placement{found[0], matches[0].ID, matches[0].PrimaryRepository}
	if err := p.Validate(); err != nil {
		return Placement{}, err
	}
	return p, nil
}

// mutation sends exactly once. Callers persist an intent before invoking it and
// retain uncertainty on any failed/partial response. It never logs daemon bodies.
func (c *Catalog) mutation(ctx context.Context, route string, data []byte, out any, statuses ...int) error {
	response, err := c.request(ctx, "POST", route, bytes.NewReader(data), 30*time.Second)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	accepted := false
	for _, status := range statuses {
		accepted = accepted || response.StatusCode == status
	}
	if !accepted {
		return fmt.Errorf("Mjolnir operation returned HTTP %d; inspect the saved intent before retrying", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(body) > maxBytes || ctx.Err() != nil {
		return errors.New("Mjolnir operation receipt is incomplete; retain its intent")
	}
	if out != nil && json.Unmarshal(body, out) != nil {
		return errors.New("Mjolnir operation receipt is invalid; retain its intent")
	}
	return nil
}
