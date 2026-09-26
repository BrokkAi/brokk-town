package mjolnir

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// RuntimePin retains the target-owned receipt explicitly selected by the
// operator. It is separate from the mutable catalog and survives profile edits.
// Its source session is discovery evidence, never ownership of a later run.
type RuntimePin struct {
	Source  SessionIdentity `json:"source"`
	Runtime RuntimeReceipt  `json:"runtime"`
}

func (p RuntimePin) Validate() error {
	data, err := json.Marshal(p.Runtime)
	if err != nil || p.Source.validate() != nil {
		return errors.New("invalid Mjolnir runtime selection")
	}
	runtime, err := readRuntimeReceipt(data)
	if err != nil || runtime == nil || runtime.ID == "" || runtime.UnavailableReason != "" {
		return errors.New("Mjolnir runtime selection requires a known target identity")
	}
	return nil
}

func (p RuntimePin) Version() string {
	var versions []string
	for _, component := range p.Runtime.Components {
		if component.Version != nil {
			versions = append(versions, component.Name+" "+*component.Version)
		}
	}
	return strings.Join(versions, "; ")
}

// DiscoverRuntime reads an operator-nominated, initialized session without
// prompting or installing a local harness. Mjolnir owns the discovery session's
// lifecycle. Later Town launches must enforce the saved opaque ID.
func (c *Catalog) DiscoverRuntime(ctx context.Context, selection Selection, session string) (RuntimePin, error) {
	if selection.Validate() != nil || !selection.Managed() {
		return RuntimePin{}, errors.New("select a Mjolnir target and profile before selecting its runtime")
	}
	receipt, err := c.readSession(ctx, session)
	if err != nil {
		return RuntimePin{}, err
	}
	if receipt.Identity.Selection != selection {
		return RuntimePin{}, errors.New("runtime discovery session belongs to a different target or profile")
	}
	if receipt.Lifecycle != "live" || receipt.ChatPhase != "idle" || !receipt.Idle || receipt.HasError == nil || *receipt.HasError {
		return RuntimePin{}, errors.New("runtime discovery session is not ready; inspect its target and harness in Mjolnir")
	}
	if receipt.Runtime == nil {
		return RuntimePin{}, errors.New("runtime receipt is unavailable; initialize the session with a Mjolnir worker supporting runtime identity")
	}
	pin := RuntimePin{receipt.Identity, *receipt.Runtime}
	if err := pin.Validate(); err != nil {
		return RuntimePin{}, err
	}
	return pin, nil
}
