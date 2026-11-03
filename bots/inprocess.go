// Package bots lists Town's bots for tests that host them in the test's own
// process. Production never does: each bot is its own binary (cmd/<bot>)
// that the service reaches over the network (internal/botapi/botclient).
package bots

import (
	bugbot "github.com/BrokkAi/brokk-town/bots/bug-bot"
	featurebot "github.com/BrokkAi/brokk-town/bots/feature-bot"
	issuebot "github.com/BrokkAi/brokk-town/bots/issue-bot"
	mayorbot "github.com/BrokkAi/brokk-town/bots/mayor-bot"
	releasebot "github.com/BrokkAi/brokk-town/bots/release-bot"
	repobot "github.com/BrokkAi/brokk-town/bots/repo-bot"
	reviewbot "github.com/BrokkAi/brokk-town/bots/review-bot"
	simplifierbot "github.com/BrokkAi/brokk-town/bots/simplifier-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// InProcess returns every bot, keyed by the role key it serves, keeping its
// workspaces under root.
func InProcess(root string) map[string]botapi.Bot {
	all := map[string]botapi.Bot{}
	for _, w := range []botapi.Worker{
		bugbot.Worker(root), featurebot.Worker(root), issuebot.Worker(root), reviewbot.Worker(root),
		releasebot.Worker(root), simplifierbot.Worker(root), repobot.Worker(root), mayorbot.Worker(root),
	} {
		all[w.Role] = w
	}
	return all
}
