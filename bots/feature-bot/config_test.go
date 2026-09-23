package featurebot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictConfigAndPaths(t *testing.T) {
	p := filepath.Join(canonicalTestDir(t), "config.json")
	for _, raw := range []string{
		`{"remote":"https://github.com/o/r.git","unknown":true}`,
		`{"remote":"https://github.com/o/r.git"} {}`,
		`{"remote":"https://github.com/o/r.git","max_issues":0}`,
		`{"remote":"https://github.com/o/r.git","max_issues":21}`,
		`{"remote":"https://github.com/o/r.git","poll":"0s"}`,
		`{"remote":"https://github.com/o/r.git","directory":"checkout","state_directory":"checkout-scans/sub"}`,
		`{"remote":"https://github.com/o/r.git","review_model":""}`,
		`{"remote":"https://github.com/o/r.git","review_model":"  "}`,
		`{"remote":"https://github.com/o/r.git","review_effort":""}`,
		`{"remote":"https://github.com/o/r.git","review_effort":"\t"}`,
	} {
		writeTestFile(t, p, raw)
		if _, err := ReadConfig(p); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	writeTestFile(t, p, `{"remote":"https://github.com/o/r.git"}`)
	c, err := ReadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.GitHubRepo() != "o/r" || !filepath.IsAbs(c.Directory) || c.MaxIssues != 3 || c.DryRun {
		t.Fatalf("bad defaults %+v", c)
	}
	if err := os.Symlink("checkout", filepath.Join(filepath.Dir(p), "alias")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, p, `{"remote":"https://github.com/o/r.git","directory":"checkout","state_directory":"alias/state"}`)
	if _, err := ReadConfig(p); err == nil {
		t.Fatal("dangling symlink overlap accepted")
	}
}
func TestRepositoryLockAcrossBranches(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", canonicalTestDir(t))
	c := DefaultConfig()
	c.Remote = "https://github.com/o/r.git"
	dir := canonicalTestDir(t)
	c.Directory = filepath.Join(dir, "a")
	c.StateDirectory = filepath.Join(dir, "a-state")
	unlock, err := lockConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	c.Branch = "other"
	c.Directory = filepath.Join(dir, "b")
	c.StateDirectory = filepath.Join(dir, "b-state")
	if release, err := lockConfig(c); err == nil {
		release()
		t.Fatal("same repository can run concurrently across branches")
	}
}

func TestReviewSelectionConfig(t *testing.T) {
	p := filepath.Join(canonicalTestDir(t), "config.json")
	for raw, want := range map[string]string{
		`{"remote":"https://github.com/o/r.git","agent":{"command":["a"],"model":"scout","effort":"high"}}`:                        "scout/high",
		`{"remote":"https://github.com/o/r.git","agent":{"command":["a"],"model":"scout","effort":"high"},"review_model":"judge"}`: "judge/high",
		`{"remote":"https://github.com/o/r.git","agent":{"command":["a"],"model":"scout","effort":"high"},"review_effort":"low"}`:  "scout/low",
		`{"remote":"https://github.com/o/r.git","agent":{"command":["a"]},"review_model":"judge","review_effort":"low"}`:           "judge/low",
	} {
		writeTestFile(t, p, raw)
		c, err := ReadConfig(p)
		if err != nil {
			t.Fatal(err)
		}
		if r := c.ReviewAgent(); r.Model+"/"+r.Effort != want || len(r.Command) != 1 || r.Command[0] != "a" {
			t.Fatalf("%s: review %+v, want %s", raw, r, want)
		}
		if strings.Contains(raw, `"model":"scout"`) && (c.Agent.Model != "scout" || c.Agent.Effort != "high") {
			t.Fatalf("%s: research changed to %+v", raw, c.Agent)
		}
	}
	for _, raw := range []string{`{"remote":"https://github.com/o/r.git","review_model":" "}`, `{"remote":"https://github.com/o/r.git","review_effort":""}`} {
		writeTestFile(t, p, raw)
		if _, err := ReadConfig(p); err == nil || !strings.Contains(err.Error(), "review_") {
			t.Fatalf("%s: %v", raw, err)
		}
	}
}
