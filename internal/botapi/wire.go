package botapi

import (
	"context"
	"errors"
)

// The contract over the network. A bot binary serves it over HTTP (see
// package botserver) and the service reaches it with package botclient:
//
//	GET  /v1/healthz   liveness; no credentials, no body worth reading
//	GET  /v1/identity  the bot's Identity
//	POST /v1/run       a Request in, a stream of Frames out
//	POST /v1/cancel    stops the run named by RunHeader
//	POST /v1/storage   a StorageRequest in, a StorageResult out
//
// Every endpoint but healthz requires "Authorization: Bearer <token>".
const (
	// RunHeader carries the caller's identifier for one run, so a later
	// cancel request can name it.
	RunHeader = "Brokk-Run-Id"
	// StreamType is the media type of a run's response: one JSON Frame per
	// line.
	StreamType = "application/x-ndjson"
)

// Bounds both sides of the network contract enforce.
const (
	// MaxRequestBytes bounds a run or storage request body.
	MaxRequestBytes = 8 << 20
	// MaxEventBytes bounds one frame other than the terminal one.
	MaxEventBytes = 1 << 20
	// MaxResultBytes bounds the terminal frame. A full repository inventory
	// carries every open issue and pull request body, so it is generous.
	MaxResultBytes = 128 << 20
	// MaxStorageBytes bounds a storage response.
	MaxStorageBytes = 32 << 20
)

// Identity is what a bot reports about itself.
type Identity struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Version string `json:"version"`
	// Contract is the contract Version the bot speaks.
	Contract int `json:"contract"`
	// Demo marks a bot that answers every run with a canned result and never
	// reaches GitHub or an agent. Only a demo service may dispatch to it.
	Demo bool `json:"demo,omitempty"`
}

// Identifier is implemented by a bot that can report its identity before a
// run, such as a bot reached over the network.
type Identifier interface {
	Identity(ctx context.Context) (Identity, error)
}

// Frame is one line of a run's response stream. Seq starts at zero and rises
// by one on every frame. The first frame carries Hello, the last carries Done,
// and the frames between carry an Event or are heartbeats that only show the
// bot is still there.
type Frame struct {
	Seq       int       `json:"seq"`
	Hello     *Identity `json:"hello,omitempty"`
	Event     *Event    `json:"event,omitempty"`
	Heartbeat bool      `json:"heartbeat,omitempty"`
	Done      *Done     `json:"done,omitempty"`
}

// Done ends a run: the result and, when the run failed, why.
type Done struct {
	Result Result `json:"result"`
	Error  string `json:"error,omitempty"`
	// Uncertain means the run was cut short by something other than the
	// service's own request, such as the bot shutting down, or its result
	// could not be delivered. The run may have acted, so the service
	// reconciles it as it does an interrupted one.
	Uncertain bool `json:"uncertain,omitempty"`
}

// ErrUncertain marks a failure after the bot may have acted on a request: the
// stream broke, ended early or carried something the contract forbids. The
// service must reconcile such a run before it repeats it, as it does after an
// interrupted one.
var ErrUncertain = errors.New("the bot's outcome is unknown")

// ErrUnavailable marks a request the bot never received: it could not be
// reached, or it refused the request before acting on it.
var ErrUnavailable = errors.New("the bot is unavailable")

// BotError is the failure a bot reported for a run it finished.
type BotError struct{ Message string }

func (e *BotError) Error() string { return e.Message }
