package main

import (
	"github.com/BrokkAi/brokk-town/bots"
	"github.com/BrokkAi/brokk-town/internal/botapi"
	"github.com/BrokkAi/brokk-town/internal/town"
)

// townBots names the bot each role dispatches to. The service reaches them
// only through the bot contract; today they are hosted in this process and
// keep their workspaces under the service's own data directory.
func townBots(dir string) map[town.Role]botapi.Bot {
	out := map[town.Role]botapi.Bot{}
	for role, bot := range bots.InProcess(dir) {
		out[town.Role(role)] = bot
	}
	return out
}
