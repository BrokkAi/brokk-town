package web

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
)

// level is what a signed-in person may do with one repository. It follows
// their access to the repository on GitHub.
type level int

const (
	// noAccess hides the repository from them entirely.
	noAccess level = iota
	// viewer (GitHub read or triage) sees the repository.
	viewer
	// operator (write or maintain) also runs its bots, decides its tasks,
	// files requests, judges outcomes and asks the Assistant.
	operator
	// repoAdmin (admin) also changes the repository's settings, placement
	// and storage, and deletes it.
	repoAdmin
)

func (l level) String() string {
	return [...]string{"none", "viewer", "operator", "admin"}[l]
}

// levelOf maps GitHub's answer to a level. A custom role is judged by the
// base permission it extends.
func levelOf(a town.RepoAccess) level {
	switch a.RoleName {
	case "admin":
		return repoAdmin
	case "maintain", "write":
		return operator
	case "triage", "read":
		return viewer
	}
	switch a.Permission {
	case "admin":
		return repoAdmin
	case "write":
		return operator
	case "read":
		return viewer
	}
	return noAccess
}

// repoAccess reads one person's access to one repository from GitHub.
type repoAccess interface {
	Access(ctx context.Context, repo, login string) (town.RepoAccess, error)
}

const (
	// accessFresh is how long an answer is used before GitHub is asked again;
	// a change on GitHub reaches Town within it.
	accessFresh = 5 * time.Minute
	// accessStale is how long an answer still stands while GitHub cannot be
	// reached, so an outage does not lock everyone out at once.
	accessStale = time.Hour
	// accessParallel bounds the lookups one request makes at a time.
	accessParallel = 8
)

type accessEntry struct {
	level level
	at    time.Time
}

// accessCache remembers each person's level per repository.
type accessCache struct {
	read    repoAccess
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]accessEntry
}

func (c *accessCache) level(ctx context.Context, repo, login string) level {
	key := strings.ToLower(repo) + "\x00" + strings.ToLower(login)
	now := c.now()
	c.mu.Lock()
	entry, cached := c.entries[key]
	c.mu.Unlock()
	if cached && now.Sub(entry.at) < accessFresh {
		return entry.level
	}
	answer, err := c.read.Access(ctx, repo, login)
	if err != nil {
		slog.Warn("could not read repository access from GitHub", "repo", repo, "login", login, "error", err)
		if cached && now.Sub(entry.at) < accessStale {
			return entry.level
		}
		return noAccess
	}
	l := levelOf(answer)
	c.mu.Lock()
	c.entries[key] = accessEntry{level: l, at: now}
	c.mu.Unlock()
	return l
}

// townAdmin reports whether the request administers Town itself: the access
// key does, and so do the logins auth.admins lists.
func (s *Server) townAdmin(r *http.Request) bool {
	login := actor(r)
	return login == "" || (s.Auth != nil && s.Auth.Admin(login))
}

// levelFor is the request's level for one repository.
func (s *Server) levelFor(r *http.Request, id string) level {
	if s.townAdmin(r) {
		return repoAdmin
	}
	cache := s.accessCache()
	if cache == nil || id == "" {
		return noAccess
	}
	return cache.level(r.Context(), strings.ToLower(id), actor(r))
}

// accessCache is created on first use, so every handler shares one.
func (s *Server) accessCache() *accessCache {
	s.accessOnce.Do(func() {
		if s.Access != nil {
			s.access = &accessCache{read: s.Access, now: time.Now, entries: map[string]accessEntry{}}
		}
	})
	return s.access
}

// levels is the request's level for every repository named, looked up in
// parallel.
func (s *Server) levels(r *http.Request, ids []string) map[string]level {
	out := make(map[string]level, len(ids))
	if s.townAdmin(r) {
		for _, id := range ids {
			out[id] = repoAdmin
		}
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, accessParallel)
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			l := s.levelFor(r, id)
			<-slots
			mu.Lock()
			out[id] = l
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

// allow answers the request itself when it lacks need for repository id. A
// repository it cannot see reads as unknown, so its existence does not leak.
func (s *Server) allow(w http.ResponseWriter, r *http.Request, id string, need level) bool {
	have := s.levelFor(r, id)
	if have >= need {
		return true
	}
	if have == noAccess {
		problem(w, "unknown repository", http.StatusNotFound)
		return false
	}
	problem(w, "this needs "+map[level]string{operator: "write", repoAdmin: "admin"}[need]+" access to the repository on GitHub", http.StatusForbidden)
	return false
}

// adminOnly guards a route that changes Town as a whole.
func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.townAdmin(r) {
			problem(w, "only a Brokk Town administrator can do this", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// visible is the part of a snapshot the request may see: the repositories it
// has access to and their events, with its level for each.
func (s *Server) visible(r *http.Request, snapshot town.State) (town.State, map[string]level) {
	ids := make([]string, 0, len(snapshot.Towns))
	for id, t := range snapshot.Towns {
		if t != nil && !t.Deleted {
			ids = append(ids, id)
		}
	}
	levels := s.levels(r, ids)
	if s.townAdmin(r) {
		return snapshot, levels
	}
	towns := map[string]*town.Town{}
	for id, t := range snapshot.Towns {
		if levels[id] > noAccess {
			towns[id] = t
		}
	}
	events := []town.Event{}
	for _, e := range snapshot.Events {
		if towns[e.Town] != nil {
			events = append(events, e)
		}
	}
	snapshot.Towns, snapshot.Events = towns, events
	return snapshot, levels
}
