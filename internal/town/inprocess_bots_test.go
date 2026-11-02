package town

import (
	"github.com/BrokkAi/brokk-town/bots"
	"github.com/BrokkAi/brokk-town/internal/botapi"
)

// inProcessBots wires the real bots for tests that drive a whole dispatch with
// fake gh, git and agents, sharing the workers' root as cmd/bt does.
func inProcessBots(root string) map[Role]botapi.Bot {
	out := map[Role]botapi.Bot{}
	for role, bot := range bots.InProcess(root) {
		out[Role(role)] = bot
	}
	return out
}

// withBots gives test workers the real bots under their own root.
func withBots(b *BotWorkers) *BotWorkers {
	b.Bots = inProcessBots(b.Root)
	return b
}

// botWorkspace is where an in-process bot keeps a repository's checkout and
// state under root.
func botWorkspace(root, id string, role Role) (string, string) {
	checkout, state, err := botapi.Dirs(root, Key(id), string(role))
	if err != nil {
		panic(err)
	}
	return checkout, state
}
