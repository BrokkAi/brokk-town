// Command townsim models Brokk Town's default scheduler with stochastic bot
// outcomes so scheduling defaults can be checked for unbounded backlog growth
// before they ship. It reproduces the supervisor's rules: one worker per house,
// a shared MaxWorkers capacity, the sixty second poll cadence, thirty minute
// discovery gaps, Simplifier intake ahead of the Mayor, one repair round per
// pull request, and a closed pull request starting its issue over.
//
// Bot-side schedules that survive Town's one-shot dispatch are folded in: Bug
// and Feature Bot save NextScan = finish + 30 min (their own Poll default), so
// a scan lands every run duration plus thirty minutes and files up to
// MaxIssues (3) issues; merges are Town's own mergeReady pass, which runs at
// the start of a Review house turn and takes that turn instead of a review;
// Release Bot only publishes releases and never touches PR or issue counts.
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"text/tabwriter"
	"time"
)

type params struct {
	days          int
	seed          int64
	maxWorkers    int
	poll          time.Duration
	discovery     time.Duration
	release       time.Duration
	mayor         time.Duration
	bugRun        time.Duration
	featureRun    time.Duration
	simplify      time.Duration
	issueWork     time.Duration
	reviewWork    time.Duration
	repairWork    time.Duration
	pMerge        float64 // overall chance a PR ends merged rather than closed
	pFixes        float64 // chance the first review asks for a fix round
	pFollowUps    float64 // chance a merged PR files two follow-up issues
	pBugAdmit     float64
	pFeatAdmit    float64
	pFollowAdmt   float64
	issuesPerScan int           // issues filed per Bug/Feature scan (bot cap: MaxIssues 3)
	mergeTurn     time.Duration // GitHub round trips for one mergeReady merge
}

type role int

const (
	bug role = iota
	simplifier
	issue
	review
	release
	feature
	nroles
)

var roleNames = [nroles]string{"bug", "simplifier", "issue", "review", "release", "feature"}

// Scheduling order mirrors town.Roles (Repo omitted: inventory is free).
var order = []role{bug, simplifier, issue, review, release, feature}

type issueT struct {
	id       int
	stage    string // simplifying, mayor, queued, working, has_pr, closed
	admit    float64
	pr       *prT
	attempts int
}

type prT struct {
	id     int
	issue  *issueT
	stage  string // simplifying, queued, reviewing, fixes, repairing, ready, merged, closed
	cycles int
}

type worker struct {
	next  time.Duration
	busy  bool
	until time.Duration
	done  func()
}

type sim struct {
	p       params
	rng     *rand.Rand
	now     time.Duration
	workers [nroles]*worker
	issues  []*issueT
	prs     []*prT
	nextID  int
	merged  int
	closed  int
	// per-day counters
	dayMerged, dayClosed, dayFiled, dayDeclined int
}

func (s *sim) active() int {
	n := 0
	for _, w := range s.workers {
		if w.busy {
			n++
		}
	}
	return n
}

func (s *sim) newIssue(admit float64) *issueT {
	s.nextID++
	it := &issueT{id: s.nextID, stage: "simplifying", admit: admit}
	s.issues = append(s.issues, it)
	s.dayFiled++
	return it
}

func (s *sim) newPR(it *issueT) *prT {
	s.nextID++
	pr := &prT{id: s.nextID, issue: it, stage: "simplifying"}
	s.prs = append(s.prs, pr)
	it.pr = pr
	it.stage = "has_pr"
	return pr
}

// nextTask mirrors town.nextTask: the lowest number in the wanted stage.
func (s *sim) nextIssueIn(stage string) *issueT {
	for _, it := range s.issues {
		if it.stage == stage {
			return it
		}
	}
	return nil
}

func (s *sim) nextPRIn(stage string) *prT {
	for _, pr := range s.prs {
		if pr.stage == stage {
			return pr
		}
	}
	return nil
}

// simplifierTask mirrors nextTask(Simplifier): PRs and issues share the queue,
// ordered by number.
func (s *sim) simplifierTask() (*issueT, *prT) {
	it := s.nextIssueIn("simplifying")
	pr := s.nextPRIn("simplifying")
	switch {
	case it == nil:
		return nil, pr
	case pr == nil:
		return it, nil
	case it.id < pr.id:
		return it, nil
	default:
		return nil, pr
	}
}

func (s *sim) start(r role, d time.Duration, done func()) {
	w := s.workers[r]
	w.busy = true
	w.until = s.now + d
	w.done = done
}

// finish applies the supervisor's post-run cadence.
func (s *sim) finish(r role) {
	w := s.workers[r]
	w.busy = false
	w.done()
	w.done = nil
	switch r {
	case bug, feature:
		w.next = s.now + s.p.discovery
	case release:
		w.next = s.now + s.p.release
	default:
		w.next = s.now + s.p.poll
	}
}

func (s *sim) dispatch(r role) {
	p := s.p
	switch r {
	case bug:
		s.start(r, p.bugRun, func() {
			for i := 0; i < p.issuesPerScan; i++ {
				s.newIssue(p.pBugAdmit)
			}
		})
	case feature:
		s.start(r, p.featureRun, func() {
			for i := 0; i < p.issuesPerScan; i++ {
				s.newIssue(p.pFeatAdmit)
			}
		})
	case simplifier:
		it, pr := s.simplifierTask()
		if it != nil {
			it.stage = "assessing"
			s.start(r, p.simplify, func() {
				// Suggest mode hands the assessment to the Mayor, who decides
				// after p.mayor. That delay is folded into the run here.
				if s.rng.Float64() < it.admit {
					it.stage = "queued"
				} else {
					it.stage = "closed"
					s.dayDeclined++
				}
			})
			return
		}
		if pr != nil {
			pr.stage = "assessing"
			s.start(r, p.simplify, func() { pr.stage = "queued" })
			return
		}
		s.workers[r].next = s.now + p.poll
	case issue:
		if pr := s.nextPRIn("fixes"); pr != nil {
			pr.stage = "repairing"
			s.start(r, p.repairWork, func() {
				pr.cycles++
				pr.stage = "queued"
			})
			return
		}
		if it := s.nextIssueIn("queued"); it != nil {
			it.stage = "working"
			it.attempts++
			s.start(r, p.issueWork, func() { s.newPR(it) })
			return
		}
		s.workers[r].next = s.now + p.poll
	case review:
		// Supervisor.execute runs mergeReady before any review. It merges the
		// lowest ready PR, reconciles, and returns; the review waits a poll.
		if ready := s.nextPRIn("ready"); ready != nil {
			ready.stage = "merging"
			s.start(r, p.mergeTurn, func() {
				ready.stage = "merged"
				ready.issue.stage = "closed"
				s.merged++
				s.dayMerged++
				if s.rng.Float64() < p.pFollowUps {
					s.newIssue(p.pFollowAdmt)
					s.newIssue(p.pFollowAdmt)
				}
			})
			return
		}
		pr := s.nextPRIn("queued")
		if pr == nil {
			s.workers[r].next = s.now + p.poll
			return
		}
		pr.stage = "reviewing"
		s.start(r, p.reviewWork, func() {
			if pr.cycles == 0 {
				if s.rng.Float64() < p.pFixes {
					pr.stage = "fixes"
				} else {
					pr.stage = "ready"
				}
				return
			}
			// Second review: calibrated so the overall merge rate is pMerge
			// given that a share pFixes of PRs reached this round.
			pSecond := (p.pMerge - (1 - p.pFixes)) / p.pFixes
			if s.rng.Float64() < pSecond {
				pr.stage = "ready"
			} else {
				// Closed after review: the issue starts over (finalizeClosedPull).
				pr.stage = "closed"
				s.closed++
				s.dayClosed++
				pr.issue.pr = nil
				pr.issue.stage = "queued"
			}
		})
	case release:
		// Release Bot publishes releases on its own daily/quiet-period pacing;
		// it never merges PRs or files issues, so it only holds a slot briefly.
		s.workers[r].next = s.now + p.release
	}
}

// tick mirrors Supervisor.schedule: roles in order, one worker per house,
// and every house except Repo counts against MaxWorkers.
func (s *sim) tick() {
	for _, r := range order {
		w := s.workers[r]
		if w.busy && s.now >= w.until {
			s.finish(r)
		}
	}
	for _, r := range order {
		w := s.workers[r]
		if w.busy || w.next > s.now {
			continue
		}
		if s.active() >= s.p.maxWorkers {
			continue
		}
		s.dispatch(r)
	}
}

func (s *sim) openIssues() (n int) {
	for _, it := range s.issues {
		if it.stage != "closed" {
			n++
		}
	}
	return n
}

func (s *sim) openPRs() (n int) {
	for _, pr := range s.prs {
		if pr.stage != "merged" && pr.stage != "closed" {
			n++
		}
	}
	return n
}

func (s *sim) queued() (issuesQueued, prsWaiting int) {
	for _, it := range s.issues {
		if it.stage == "queued" || it.stage == "simplifying" {
			issuesQueued++
		}
	}
	for _, pr := range s.prs {
		if pr.stage == "queued" || pr.stage == "fixes" || pr.stage == "simplifying" || pr.stage == "ready" {
			prsWaiting++
		}
	}
	return
}

func main() {
	p := params{}
	flag.IntVar(&p.days, "days", 30, "days to simulate")
	flag.Int64Var(&p.seed, "seed", 1, "random seed")
	flag.IntVar(&p.maxWorkers, "max-workers", 4, "ServiceConfig.MaxWorkers")
	flag.DurationVar(&p.poll, "poll", 60*time.Second, "Config.PollSeconds")
	flag.DurationVar(&p.discovery, "discovery", 30*time.Minute, "gap between Bug/Feature scans")
	flag.DurationVar(&p.release, "release", 5*time.Minute, "gap between Release passes")
	flag.DurationVar(&p.mayor, "mayor", time.Second, "Mayor decision time")
	flag.DurationVar(&p.bugRun, "bug-run", 10*time.Minute, "Bug Bot scan duration (assumed)")
	flag.DurationVar(&p.featureRun, "feature-run", 10*time.Minute, "Feature Bot scan duration (assumed)")
	flag.DurationVar(&p.simplify, "simplify", 5*time.Minute, "Simplifier assessment duration (assumed)")
	flag.DurationVar(&p.issueWork, "issue-work", 30*time.Minute, "Issue Bot time per issue")
	flag.DurationVar(&p.reviewWork, "review-work", 15*time.Minute, "Review Bot time per review")
	flag.DurationVar(&p.repairWork, "repair-work", 30*time.Minute, "Issue Bot time per fix round")
	flag.Float64Var(&p.pMerge, "p-merge", 0.8, "overall chance a PR is merged")
	flag.Float64Var(&p.pFixes, "p-fixes", 0.9, "chance the first review needs fixes")
	flag.Float64Var(&p.pFollowUps, "p-followups", 0.1, "chance a merged PR files two follow-up issues")
	flag.Float64Var(&p.pBugAdmit, "p-bug-admit", 0.9, "Simplifier admits a Bug Bot issue")
	flag.Float64Var(&p.pFeatAdmit, "p-feature-admit", 0.25, "Simplifier admits a Feature Bot issue")
	flag.Float64Var(&p.pFollowAdmt, "p-followup-admit", 0.9, "Simplifier admits a review follow-up (assumed)")
	flag.IntVar(&p.issuesPerScan, "issues-per-scan", 1, "issues each Bug/Feature scan files (bot cap MaxIssues is 3)")
	flag.DurationVar(&p.mergeTurn, "merge-turn", time.Minute, "Review house time spent merging one ready PR")
	flag.Parse()
	p.simplify += p.mayor

	s := &sim{p: p, rng: rand.New(rand.NewSource(p.seed))}
	for i := range s.workers {
		s.workers[i] = &worker{}
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "day\tmerged (day)\tmerged (total)\tclosed (total)\tPRs open\tissues open\tissues filed (day)\tissues declined (day)\t")
	day := 24 * time.Hour
	for d := 1; d <= p.days; d++ {
		end := time.Duration(d) * day
		for s.now < end {
			s.tick()
			s.now += time.Second
		}
		fmt.Fprintf(tw, "%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t\n", d, s.dayMerged, s.merged, s.closed, s.openPRs(), s.openIssues(), s.dayFiled, s.dayDeclined)
		s.dayMerged, s.dayClosed, s.dayFiled, s.dayDeclined = 0, 0, 0, 0
	}
	tw.Flush()

	// Steady-state demand on the single Issue house, the serial bottleneck.
	hour := float64(time.Hour)
	bugRate := hour / float64(p.bugRun+p.discovery) * float64(p.issuesPerScan) * p.pBugAdmit
	featRate := hour / float64(p.featureRun+p.discovery) * float64(p.issuesPerScan) * p.pFeatAdmit
	admitted := bugRate + featRate
	attemptsPerMerge := 1 / p.pMerge
	followups := 2 * p.pFollowUps * p.pFollowAdmt
	issueMinutesPerAttempt := (float64(p.issueWork) + p.pFixes*float64(p.repairWork)) / float64(time.Minute)
	reviewMinutesPerAttempt := ((1+p.pFixes)*float64(p.reviewWork) + p.pMerge*float64(p.mergeTurn+p.poll)) / float64(time.Minute)
	demand := admitted / (1 - followups) * attemptsPerMerge
	fmt.Printf("\nAdmitted work: %.2f issues/hour from Bug and Feature (follow-ups multiply that by %.2f)\n", admitted, 1/(1-followups))
	fmt.Printf("PR attempts needed: %.2f/hour; each attempt costs the Issue house %.0f min and the Review house %.1f min\n", demand, issueMinutesPerAttempt, reviewMinutesPerAttempt)
	fmt.Printf("Issue house utilization: %.0f%%   Review house utilization: %.0f%%\n", demand*issueMinutesPerAttempt/60*100, demand*reviewMinutesPerAttempt/60*100)
	q, w := s.queued()
	fmt.Printf("End state: %d issues waiting for Issue Bot, %d PRs waiting for Review/Issue Bot\n", q, w)
}
