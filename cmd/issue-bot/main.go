// Command issue-bot serves issue-bot over the bot contract's network API. See
// docs/operations.md for its flags and environment.
package main

import (
	issuebot "github.com/BrokkAi/brokk-town/bots/issue-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

var version = "dev"

func main() { botserver.Main(version, issuebot.Worker) }
