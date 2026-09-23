package featurebot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"text/tabwriter"
	"time"
)

// ProposalSelector is the stable public selector of a saved candidate. It is
// derived from the candidate's request ID without revealing the hidden marker.
func ProposalSelector(c *Candidate) string {
	sum := sha256.Sum256([]byte("feature-bot proposal\x00" + c.RequestID))
	return fmt.Sprintf("%x", sum[:6])
}

// WriteProposalList lists saved dry-run proposals, and any selected proposal
// whose publication is unfinished, with their selectors and eligibility. It
// only reads state: no fetch, GitHub request or agent.
func WriteProposalList(w io.Writer, state *State) error {
	type row struct{ selector, eligibility, commit, title string }
	var rows []row
	add := func(c *Candidate, eligibility string) {
		commit := "-"
		if c.Commit != "" {
			commit = c.Commit[:12]
		}
		rows = append(rows, row{ProposalSelector(c), eligibility, commit, c.Finding.Title})
	}
	if state != nil {
		var unresolved *Candidate
		if p := state.Publication; p != nil {
			if c := completedCandidate(state, p.RequestID); c != nil && c.Status == "posting" {
				unresolved = c
			}
		}
		for _, c := range state.Completed {
			active := state.Publication != nil && state.Publication.RequestID == c.RequestID
			if c.Status != "dry_run" && !active {
				continue
			}
			switch {
			case active && c.Status == "posting":
				add(c, "unknown outcome: publish again to reconcile")
			case state.Scan != nil:
				add(c, "blocked: unrelated active scan")
			case unresolved != nil:
				add(c, "blocked: unknown outcome of "+ProposalSelector(unresolved))
			case c.Commit == "":
				add(c, "ineligible: no recorded source commit")
			case active:
				add(c, "eligible: resumes unfinished publication")
			default:
				add(c, "eligible")
			}
		}
		if state.Scan != nil {
			for _, c := range state.Scan.Candidates {
				if c.Status == "dry_run" {
					add(c, "ineligible: scan still active")
				}
			}
		}
	}
	if len(rows) == 0 {
		_, err := io.WriteString(w, "No saved dry-run proposals.\n")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SELECTOR\tELIGIBILITY\tCOMMIT\tTITLE")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.selector, r.eligibility, r.commit, r.title)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\nPublication also requires the branch to still be at the recorded commit.\n")
	return err
}

// Publish files exactly one selected saved dry-run proposal after a fresh
// independent review in an isolated workspace at its recorded commit. It never
// starts discovery. Repeating a successful selection reports the saved outcome.
func Publish(ctx context.Context, cfg Config, log *slog.Logger, selector string, out io.Writer) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.GitHubRepo() == "" {
		return errors.New("GitHub repository required; set github.repo for a local mirror")
	}
	if log == nil {
		log = slog.Default()
	}
	e := engine{config: cfg, source: githubClient{cfg}, log: log, agent: func(c Config, stage string) Agent { return agentProcess{c, log.With("stage", stage), stage} }, now: time.Now}
	return e.publishSaved(ctx, selector, out)
}

func (e engine) publishSaved(ctx context.Context, selector string, out io.Writer) error {
	// Publication is deliberate; a dry_run setting in shared configuration applies to scans.
	e.config.DryRun = false
	unlock, err := lockConfig(e.config)
	if err != nil {
		return err
	}
	defer unlock()
	s, err := ReadState(e.config)
	if err != nil {
		return err
	}
	if s == nil {
		return errors.New("no saved proposals; run a dry run first (bfb once --dry-run)")
	}
	c, err := e.publish(ctx, s, selector)
	if c != nil {
		switch c.Status {
		case "submitted":
			fmt.Fprintf(out, "Proposal %s published: %s\n", selector, c.URL)
		case "duplicate", "uncertain", "invalid":
			fmt.Fprintf(out, "Proposal %s not published (%s", selector, c.Status)
			if c.URL != "" {
				fmt.Fprintf(out, ", %s", c.URL)
			}
			fmt.Fprintf(out, "): %s\n", c.Review)
		}
	}
	return err
}

// publish processes the selected proposal and returns it once resolved.
func (e engine) publish(ctx context.Context, s *State, selector string) (*Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var c *Candidate
	for _, candidate := range s.Completed {
		if ProposalSelector(candidate) == selector {
			if c != nil {
				return nil, fmt.Errorf("proposal selector %s is ambiguous", selector)
			}
			c = candidate
		}
	}
	// An unknown create outcome only needs its marker; no workspace or review
	// is involved, so reconcile it even while a scan is active.
	if c != nil && c.Status == "posting" {
		return c, e.reconcilePublication(ctx, s, c)
	}
	if s.Scan != nil {
		for _, candidate := range s.Scan.Candidates {
			if ProposalSelector(candidate) == selector {
				return nil, fmt.Errorf("proposal %s belongs to the active scan; finish it with bfb once first", selector)
			}
		}
		return nil, errors.New("an unrelated scan is active; finish it with bfb once (or wait for the daemon) before publishing a saved proposal")
	}
	if c == nil {
		return nil, fmt.Errorf("no saved proposal %s; list selectors with bfb publish --list", selector)
	}
	if p := s.Publication; p != nil && p.RequestID != c.RequestID {
		if other := completedCandidate(s, p.RequestID); other.Status == "posting" {
			return nil, fmt.Errorf("publication of proposal %s has an unknown outcome; run bfb publish --proposal %s to reconcile it first", ProposalSelector(other), ProposalSelector(other))
		}
		// An unfinished selection without a create request can be replaced; its
		// proposal keeps its dry_run status and findings, and its worktree
		// becomes prunable.
		e.log.Info("Replacing unfinished proposal selection", "previous", ProposalSelector(completedCandidate(s, p.RequestID)))
		e.retireWorkspace(s)
		s.Publication = nil
	}
	resumed := s.Publication != nil
	if resumed && c.Status != "dry_run" {
		// The outcome was saved but the selection was not retired (interrupted
		// or failed save). Report it; never review or create again.
		return c, e.completePublication(s, c, selector)
	}
	if !resumed {
		switch {
		case c.Status == "submitted":
			return c, nil
		case c.Status != "dry_run":
			return c, fmt.Errorf("proposal %s has status %s; only saved dry-run proposals can be published", selector, c.Status)
		case c.Commit == "":
			return nil, fmt.Errorf("proposal %s was saved without its source commit by an older release and cannot be verified; run bfb once --dry-run to research it again", selector)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(e.config.Timeout))
	defer cancel()
	fail := func(err error) (*Candidate, error) {
		if s.Publication != nil {
			s.Publication.Failure = err.Error()
			err = errors.Join(err, e.save(s))
		}
		return nil, err
	}
	g := checkout{e.config}
	if err := g.open(ctx); err != nil {
		return fail(err)
	}
	head, err := g.head(ctx)
	if err != nil {
		return fail(err)
	}
	if head != c.Commit {
		return fail(fmt.Errorf("branch %s is at %s but proposal %s was researched at %s; publication refused and no replacement discovery started", e.config.Branch, head, selector, c.Commit))
	}
	if !resumed {
		// A fresh review must not reuse the dry run's review progress.
		c.Checkpoint = nil
		s.Publication = &Publication{RequestID: c.RequestID, Commit: c.Commit}
	}
	// The selection owns one isolated worktree; retries revalidate the same one.
	if s.Publication.Directory == "" {
		var id [12]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		s.Publication.Directory = filepath.Join(e.config.Directory+"-scans", fmt.Sprintf("scan-%x", id))
	}
	s.Publication.Failure = ""
	if err := e.save(s); err != nil {
		return nil, err
	}
	scan := &Scan{Commit: c.Commit, Directory: s.Publication.Directory}
	w, err := g.prepare(ctx, scan)
	if err != nil {
		return fail(err)
	}
	if err := w.verifyFiles(ctx, c.Finding); err != nil {
		return fail(err)
	}
	cfg := w.config.review()
	e.log.Info("Reviewing selected proposal", "stage", "review", "proposal", selector, "model", selection(cfg.Agent.Model), "effort", selection(cfg.Agent.Effort), "commit", c.Commit)
	if err := e.reviewAndPublish(ctx, s, scan, g, w, e.agent(cfg, "review"), c); err != nil {
		if errors.Is(err, errBranchAdvanced) {
			err = fmt.Errorf("%w; publication refused and no replacement discovery started", err)
		}
		return fail(err)
	}
	return c, e.completePublication(s, c, selector)
}

// reconcilePublication resolves an interrupted create by its request marker and
// never sends another create request.
func (e engine) reconcilePublication(ctx context.Context, s *State, c *Candidate) error {
	issues, err := e.source.issues(ctx)
	if err != nil {
		return err
	}
	for _, i := range issues {
		if containsMarker(i, c.RequestID) {
			if err := validateCreated(e.config, c, &i); err != nil {
				return err
			}
			c.Status = "submitted"
			c.URL = i.URL
			return e.completePublication(s, c, ProposalSelector(c))
		}
	}
	return errors.New("issue creation outcome is unknown; no marker visible yet, refusing to repost (inspect saved state and GitHub)")
}

// retireWorkspace records the selection's worktree for prune, which removes it
// only when it is clean at the recorded commit, and retires a missing one.
func (e engine) retireWorkspace(s *State) {
	if p := s.Publication; p != nil && p.Directory != "" {
		s.Workspaces = append(s.Workspaces, CompletedWorkspace{Directory: p.Directory, Commit: p.Commit, CompletedAt: e.now()})
	}
}

// completePublication retires a resolved selection; its workspace becomes prunable.
func (e engine) completePublication(s *State, c *Candidate, selector string) error {
	if s.Publication != nil && s.Publication.RequestID == c.RequestID {
		e.retireWorkspace(s)
		s.Publication = nil
	}
	if err := e.save(s); err != nil {
		return err
	}
	if c.Status != "submitted" {
		return fmt.Errorf("proposal %s was not published: review verdict %s", selector, c.Status)
	}
	return nil
}
