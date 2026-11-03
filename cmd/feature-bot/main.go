// Command feature-bot serves feature-bot over the bot contract's network API. See
// docs/operations.md for its flags and environment.
package main

import (
	featurebot "github.com/BrokkAi/brokk-town/bots/feature-bot"
	"github.com/BrokkAi/brokk-town/internal/botapi/botserver"
)

var version = "dev"

func main() { botserver.Main(version, featurebot.Worker) }
