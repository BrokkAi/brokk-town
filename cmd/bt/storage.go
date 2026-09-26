package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func storageCommand(ctx context.Context, dir string, fl *cliFlags) error {
	if *fl.repo == "" {
		return errors.New("storage requires --repo OWNER/REPO")
	}
	if *fl.storageAge < 0 || *fl.storageAge > 87600 {
		return errors.New("--older-than-hours must be between 0 and 87600")
	}
	body := map[string]any{"town": *fl.repo, "minimum_age_hours": *fl.storageAge}
	conn, err := requireService(ctx, dir)
	if err != nil {
		return err
	}
	if *fl.storageCleanup != "" {
		body["ids"] = strings.Split(*fl.storageCleanup, ",")
		var result []town.StorageRemoval
		if err := request(ctx, conn, "POST", "/api/storage/cleanup", body, &result); err != nil {
			return err
		}
		if *fl.json {
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		for _, r := range result {
			fmt.Printf("%s: %s — %s\n", r.ID, r.Status, r.Detail)
		}
		return nil
	}
	var result town.StorageInventory
	if err := request(ctx, conn, "POST", "/api/storage", body, &result); err != nil {
		return err
	}
	if *fl.json {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("%s storage · dry run · minimum age %d hours\n", result.Town, result.MinimumAgeHours)
	for _, role := range town.AgentRoles {
		if usage, ok := result.Roles[role]; ok {
			fmt.Printf("%s: %d bytes, %d files, %d artifacts\n", role, usage.Bytes, usage.Files, usage.Artifacts)
		}
	}
	if result.CleanupHold != "" {
		fmt.Println(result.CleanupHold)
	}
	for _, a := range result.Artifacts {
		disposition := "retained"
		if a.Eligible {
			disposition = "eligible"
		}
		fmt.Printf("%s  %s  %d bytes  %s  %s  %s\n  %s (%s): %s\n", a.ID, disposition, a.Bytes, a.Modified.Format("2006-01-02T15:04:05Z07:00"), a.Role, a.Task, a.Path, a.Kind, a.Reason)
	}
	if result.Incomplete {
		fmt.Println("Inventory incomplete; no artifacts can be removed.")
	}
	return nil
}
