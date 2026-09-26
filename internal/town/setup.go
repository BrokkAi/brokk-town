package town

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/BrokkAi/brokk-town/internal/harness"
)

// Diagnose only looks up executables and reads GitHub metadata. In particular,
// it never prepares a runtime, starts an agent, or executes a verifier. The
// service handles it on demand, outside scheduling and client render loops.
func (s *Supervisor) Diagnose(ctx context.Context, id string) (*DiagnosticReport, error) {
	caller := ctx
	id = strings.ToLower(id)
	state := s.Store.Snapshot()
	t := state.Towns[id]
	if t == nil || t.Deleted {
		return nil, errors.New("unknown town")
	}
	s.mu.Lock()
	if s.diagnosing {
		s.mu.Unlock()
		return nil, errors.New("a setup diagnostic is already running; try again when it finishes")
	}
	s.diagnosing = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.diagnosing = false; s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	report := &DiagnosticReport{At: s.now().UTC()}
	if state.Demo {
		report.Checks = []Diagnostic{{Code: "demo", Status: "unknown", Detail: "Demo uses simulated workers and GitHub; live setup has not been checked.", Action: "Run diagnostics in a non-demo Town to check its setup."}}
	} else {
		report.Checks = s.setupChecks(ctx, t.Config)
	}
	// Our own read deadlines produce unknown checks. A canceled or expired
	// caller, however, must not replace the last completed diagnostic.
	if err := caller.Err(); err != nil {
		return nil, err
	}
	err := s.Store.Update(func(st *State) error {
		if err := caller.Err(); err != nil {
			return err
		}
		current := st.Towns[id]
		if current == nil || current.Deleted {
			return errors.New("town was deleted during diagnostics")
		}
		if !reflect.DeepEqual(current.Config, t.Config) {
			return errors.New("settings changed during diagnostics; run them again")
		}
		current.Diagnostics = clone(report)
		return nil
	})
	return report, err
}

func (s *Supervisor) setupChecks(ctx context.Context, c Config) []Diagnostic {
	var checks []Diagnostic
	add := func(code string, role Role, status, detail, action string) {
		checks = append(checks, Diagnostic{Code: code, Role: role, Status: status, Detail: detail, Action: action})
	}
	for _, bin := range []string{"git", "gh", "npx"} {
		if _, err := exec.LookPath(bin); err != nil {
			add(bin, "", "blocked", bin+" is unavailable on the Town service PATH.", "Install "+bin+" and restart Town with a PATH that includes it.")
		} else {
			add(bin, "", "passed", bin+" is available on the Town service PATH.", "")
		}
	}
	_, ghErr := exec.LookPath("gh")
	if ghErr != nil || s.GitHub == nil {
		add("github_auth", "", "unknown", "GitHub authentication could not be checked.", "Make gh available, authenticate with gh auth login, and run diagnostics again.")
	} else {
		readCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		actor, err := s.GitHub.Actor(readCtx)
		cancel()
		if err != nil || actor == "" {
			add("github_auth", "", "unknown", "GitHub could not confirm the service account's authentication.", "Check network access and run gh auth status or gh auth login as the Town service user.")
		} else {
			add("github_auth", "", "passed", "GitHub confirmed an authenticated service account.", "")
		}
	}
	reader, ok := s.GitHub.(SetupGitHub)
	if ghErr != nil || !ok {
		add("repository", "", "unknown", "Repository access could not be checked.", "Check gh availability and repository permissions, then run diagnostics again.")
	} else {
		readCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := reader.SetupAccess(readCtx, c.Repo)
		cancel()
		if err != nil {
			add("repository", "", "unknown", "Repository access could not be confirmed; permissions or network access may be unavailable.", "Open the repository as the service account and check organization/SSO access with gh.")
		} else {
			add("repository", "", "passed", "The service account can read this repository's metadata. Write permissions and Git transport are not tested.", "")
		}
	}
	checks[len(checks)-1].URL = "https://github.com/" + c.Repo
	for _, role := range AgentRoles {
		cfg := c.ForRole(role)
		status, detail, action := agentSetup(cfg, s.Harnesses)
		add("agent", role, status, detail, action)
		add("agent_auth", role, "unknown", "Agent authentication is not detectable without starting the configured harness.", "Use the harness's own authentication/status command as the Town service user; no agent session was started.")
		verify := c.Verify
		if p := c.BotPolicies[role]; len(p.Verify) > 0 {
			verify = p.Verify
		}
		if len(verify) == 0 {
			add("verify", role, "unknown", "No explicit verification command is configured; the bot may use its own defaults.", "Set this house's verification command if you require specific checks.")
		} else {
			status, detail, action = commandSetup(verify[0], "Verification command")
			add("verify", role, status, detail+" It was not executed.", action)
		}
		if p := c.BotPolicies[role]; p.Release != nil && len(p.Release.Preflight) > 0 {
			status, detail, action = commandSetup(p.Release.Preflight[0], "Release preflight command")
			add("preflight", role, status, detail+" It was not executed.", action)
		}
	}
	return checks
}

func commandSetup(command, label string) (string, string, string) {
	// Relative commands run in a prepared checkout, which a read-only setup
	// probe must not create or pretend is the service's working directory.
	if strings.ContainsRune(command, '/') && !filepath.IsAbs(command) {
		return "unknown", label + " uses a checkout-relative executable.", "Verify the executable exists and is executable in the bot's checkout before starting work."
	}
	if _, err := exec.LookPath(command); err != nil {
		return "blocked", label + " executable is unavailable to the Town service.", "Check its configured executable and the service PATH, then run diagnostics again."
	}
	return "passed", label + " executable is available.", ""
}

func agentSetup(c Config, catalog *harness.Catalog) (string, string, string) {
	if len(c.Agent.Command) > 0 {
		return commandSetup(c.Agent.Command[0], "Agent command")
	}
	var entry harness.Entry
	if c.HarnessDefinition != nil {
		entry = *c.HarnessDefinition
	} else {
		if catalog == nil {
			return "unknown", "Harness catalog is unavailable.", "Refresh the harness catalog and reselect the desired version."
		}
		var err error
		entry, err = catalog.Lookup(c.harness(), "")
		if err != nil {
			return "unknown", "The selected harness is not in the cached catalog.", "Refresh the harness catalog and select a supported harness."
		}
	}
	if len(entry.Command) > 0 {
		return commandSetup(entry.Command[0], "Agent command")
	}
	for _, option := range []struct {
		bin string
		pkg *harness.Package
	}{{"npx", entry.Distribution.Npx}, {"uvx", entry.Distribution.Uvx}} {
		if option.pkg == nil {
			continue
		}
		status, detail, action := commandSetup(option.bin, "Agent package runner ("+option.bin+")")
		if status != "passed" {
			return status, detail, action
		}
		return "unknown", detail + " The selected agent package was not installed or started by this check.", "Check the pinned package's installation and authentication before starting work."
	}
	if _, ok := entry.Distribution.Binary[harness.Platform()]; !ok {
		return "blocked", "The selected harness has no distribution for this platform.", "Choose a supported harness or configure a locally installed custom command."
	}
	return "unknown", "The selected native agent archive was not prepared or started by this check.", "Check the pinned runtime's installation and authentication before starting work."
}
