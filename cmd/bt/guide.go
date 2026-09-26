package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func guideCommand(ctx context.Context, dir string, fl *cliFlags) error {
	if *fl.repo == "" {
		return errors.New("guide requires --repo OWNER/REPO")
	}
	if *fl.guideDigest != "" && !*fl.guideConfirm {
		return errors.New("--proposal-digest requires --confirm")
	}
	modes := 0
	for _, on := range []bool{*fl.guideAsk != "", *fl.guideCancel, *fl.guideConfirm, *fl.check} {
		if on {
			modes++
		}
	}
	if modes > 1 {
		return errors.New("choose one of --ask, --check, --cancel, or --confirm")
	}
	if (*fl.guideConfirm || *fl.guideCancel || *fl.check) && *fl.requestID == "" {
		return errors.New("--check, --cancel and --confirm require --request-id")
	}
	if *fl.guideAsk != "" && *fl.requestID != "" {
		return errors.New("use --check with a saved submission ID; --ask creates a new question")
	}
	conn, err := requireService(ctx, dir)
	if err != nil {
		return err
	}
	var conversation town.GuideConversation
	read := func() error {
		return request(ctx, conn, "POST", "/api/guide", map[string]any{"town": *fl.repo, "action": "read"}, &conversation)
	}
	if err := read(); err != nil {
		return err
	}
	body := map[string]any{"town": *fl.repo}
	id := *fl.requestID
	if *fl.guideAsk != "" {
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return err
		}
		id = hex.EncodeToString(bytes)
		body["action"], body["id"], body["sequence"], body["question"] = "ask", id, conversation.Next, *fl.guideAsk
		if *fl.json {
			fmt.Fprintln(os.Stderr, "Town Guide submission:", id)
		} else {
			fmt.Println("Town Guide submission:", id)
		}
	} else if *fl.guideCancel {
		body["action"], body["id"] = "cancel", id
	} else if *fl.guideConfirm {
		var proposal *town.GuideProposal
		for _, turn := range conversation.Turns {
			if turn.ID == id {
				proposal = turn.Proposal
			}
		}
		if proposal == nil {
			return errors.New("no saved proposal for this question")
		}
		if *fl.guideDigest == "" || *fl.guideDigest != proposal.Digest {
			return errors.New("inspect the proposal, then --confirm --request-id ID --proposal-digest DIGEST")
		}
		body["action"], body["id"], body["digest"] = "confirm", id, *fl.guideDigest
	} else if *fl.guideDigest != "" {
		return errors.New("--proposal-digest requires --confirm")
	}
	if body["action"] != nil {
		if err := request(ctx, conn, "POST", "/api/guide", body, &conversation); err != nil {
			return fmt.Errorf("guide request failed; inspect --check --request-id %s before submitting again: %w", id, err)
		}
	}
	if *fl.json {
		data, _ := json.MarshalIndent(conversation, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	if id == "" {
		for _, turn := range conversation.Turns {
			printGuideTurn(turn)
		}
		if len(conversation.Turns) == 0 {
			fmt.Println("Town Guide at Town Hall. Ask with --ask 'What needs attention?'")
		}
		return nil
	}
	shown := ""
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var found *town.GuideTurn
		for i := range conversation.Turns {
			if conversation.Turns[i].ID == id {
				found = &conversation.Turns[i]
				break
			}
		}
		if found == nil {
			return errors.New("question is no longer in the recent conversation; it was not resubmitted")
		}
		if len(found.Answer) >= len(shown) && found.Answer[:len(shown)] == shown {
			fmt.Print(found.Answer[len(shown):])
		} else if found.Answer != shown {
			fmt.Print("\n", found.Answer)
		}
		shown = found.Answer
		if found.Status != "queued" && found.Status != "gathering" && found.Status != "answering" {
			fmt.Println()
			fmt.Println("Town Guide:", found.Status, found.Detail)
			printGuideProposal(*found)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("stopped watching; the question remains in Town. Reconnect with --check --request-id %s or cancel it explicitly", id)
		case <-ticker.C:
		}
		if err := read(); err != nil {
			return err
		}
	}
}
func printGuideTurn(turn town.GuideTurn) {
	fmt.Printf("\nYou [%s]: %s\nTown Guide [%s]: %s\n%s\n", turn.ID, turn.Question, turn.Status, turn.Answer, turn.Detail)
	printGuideProposal(turn)
}
func printGuideProposal(turn town.GuideTurn) {
	if p := turn.Proposal; p != nil {
		fmt.Printf("Proposal [%s]: %s\nConfirm: --confirm --request-id %s --proposal-digest %s\n", p.Status, p.Description, turn.ID, p.Digest)
	}
}
