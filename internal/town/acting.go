package town

import (
	"context"
	"errors"
	"time"

	"github.com/BrokkAi/brokk-town/internal/auth"
	"github.com/BrokkAi/brokk-town/internal/mjolnir"
)

// Acting is the supervisor acting for one person. The events an action
// through it appends, the issue it files and the judgment it records name
// that person's GitHub login. An empty login is the local access key, which
// records no one, as Town always has.
type Acting struct {
	s     *Supervisor
	actor string
}

// As returns the supervisor acting for login, a GitHub login that sign-in
// already checked; an invalid one acts as the local access key.
func (s *Supervisor) As(login string) Acting {
	if !ValidLogin(login) {
		login = ""
	}
	return Acting{s: s, actor: login}
}

// ValidLogin reports whether login has the shape of a GitHub login.
func ValidLogin(login string) bool { return auth.ValidLogin(login) }

// decider is how a decision names the person who made it.
func (a Acting) decider() string {
	if a.actor == "" {
		return "You"
	}
	return a.actor
}

// JudgeOutcome records a usefulness judgment on one filed finding.
func (a Acting) JudgeOutcome(id, outcome, value, explanation string) error {
	return a.s.Store.Update(func(st *State) error {
		t := st.Towns[id]
		if t == nil || t.Deleted {
			return errors.New("unknown repository")
		}
		return t.judgeOutcome(a.actor, outcome, value, explanation, a.s.now())
	})
}

// The supervisor's own methods act for the local access key.

func (s *Supervisor) Control(id string, role Role, action, taskID string) error {
	return s.As("").Control(id, role, action, taskID)
}
func (s *Supervisor) Defer(id, taskID string, until time.Time, reason string) error {
	return s.As("").Defer(id, taskID, until, reason)
}
func (s *Supervisor) AddRepo(repo, mergePolicy string, settings AgentSettings) (string, error) {
	return s.As("").AddRepo(repo, mergePolicy, settings)
}
func (s *Supervisor) Delete(id string) error { return s.As("").Delete(id) }
func (s *Supervisor) ApplySettings(id string, role Role, settings AgentSettings, edits TownSettings) error {
	return s.As("").ApplySettings(id, role, settings, edits)
}
func (s *Supervisor) SubmitRequest(id string, input IssueRequest) (*IssueRequest, error) {
	return s.As("").SubmitRequest(id, input)
}
func (s *Supervisor) SetExecution(id string, role Role, selection *mjolnir.Selection) error {
	return s.As("").SetExecution(id, role, selection)
}
func (s *Supervisor) SelectExecutionRuntime(ctx context.Context, id string, role Role, session string) error {
	return s.As("").SelectExecutionRuntime(ctx, id, role, session)
}
func (s *Supervisor) ConfirmGuide(id, turnID, digest string) error {
	return s.As("").ConfirmGuide(id, turnID, digest)
}
