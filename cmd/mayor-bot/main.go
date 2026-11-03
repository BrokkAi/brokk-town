// Command mayor-bot serves mayor-bot over the bot contract's network API. See
// docs/operations.md for its flags and environment.
package main

import (
	mayorbot "github.com/BrokkAi/brokk-town/bots/mayor-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

var version = "dev"

func main() { botserver.Main(version, mayorbot.Worker) }
