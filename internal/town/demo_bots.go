package town

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// DemoBots sends every bot a demo run through the bot contract, at start and
// then every few minutes, and records each answer in Activity. Only a bot
// started in demo mode is ever dispatched to, so the demo still never reaches
// GitHub or an agent; what it proves is that each bot answers over the
// transport the service really uses. It returns only when ctx ends.
func DemoBots(ctx context.Context, store *Store, b *BotWorkers) error {
	return runDemoBots(ctx, store, b, 5*time.Second, 5*time.Minute)
}

func runDemoBots(ctx context.Context, store *Store, b *BotWorkers, first, interval time.Duration) error {
	if !store.Snapshot().Demo {
		return errors.New("demo dispatch requires an isolated demo store")
	}
	last := map[Role]string{}
	wait := first
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = interval
		snapshot := store.Snapshot()
		ids := make([]string, 0, len(snapshot.Towns))
		for id := range snapshot.Towns {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) == 0 || len(b.Bots) == 0 {
			continue
		}
		t := snapshot.Towns[ids[0]]
		for _, role := range AgentRoles {
			if b.Bots[role] == nil {
				continue
			}
			title, kind := demoDispatch(ctx, b, t, role)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Repeat a failure only when it changes, so an absent bot does
			// not fill Activity.
			if kind == "error" && last[role] == title {
				continue
			}
			last[role] = title
			if err := store.Update(func(st *State) error {
				st.Event(t.ID, kind, string(role), "hall", "", title, time.Now())
				return nil
			}); err != nil {
				return err
			}
		}
	}
}

// demoDispatch sends one role's bot a demo run and checks the result the way
// a real dispatch is checked.
func demoDispatch(ctx context.Context, b *BotWorkers, t *Town, role Role) (title, kind string) {
	name := botName(role)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	version, err := b.botIdentity(ctx, role)
	if err != nil {
		return fmt.Sprintf("%s could not be reached: %s", name, demoReason(err)), "error"
	}
	head := strings.Repeat("a", 40)
	request := workerRequest{Town: Key(t.ID), Remote: "https://github.com/" + t.Config.Repo + ".git", Branch: t.Branch(), Repo: t.Config.Repo, Host: "github.com"}
	switch role {
	case Review:
		request.PR, request.BaseSHA, request.HeadSHA = 1, strings.Repeat("b", 40), head
	case Simplifier:
		request.Issue, request.Mode = 1, "suggest"
	case Hall:
		request.Mode, request.Issue, request.Arrival = "judge", 1, json.RawMessage(`{"kind":"issue","title":"Demo arrival"}`)
	case Repo:
		request.Mode = "inventory"
	}
	started := time.Now()
	result, err := b.dispatchBot(ctx, role, request, false, nil)
	if err == nil {
		err = validateWorkerResult(result, role)
	}
	if err == nil && role == Repo {
		_, err = repoResult(result, nil)
	}
	if err == nil && role == Review && (result.Review == nil || result.Review.ExactHead != head) {
		err = errors.New("the review did not cover the requested revision")
	}
	if err != nil {
		return fmt.Sprintf("%s answered with an error: %s", name, demoReason(err)), "error"
	}
	return fmt.Sprintf("%s %s answered a demo dispatch over the network in %s", name, version, time.Since(started).Round(time.Millisecond)), "activity"
}

func demoReason(err error) string { return botapi.Truncate(err.Error(), 300) }
