// Command bug-bot serves bug-bot over the bot contract's network API. See
// docs/operations.md for its flags and environment.
package main

import (
	bugbot "github.com/BrokkAi/brokk-town/bots/bug-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

var version = "dev"

func main() { botserver.Main(version, bugbot.Worker) }
