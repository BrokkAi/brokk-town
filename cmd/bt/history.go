package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/BrokkAi/brokk-town/internal/town"
)

func historyCommand(ctx context.Context, dir string, fl *cliFlags) error {
	if *fl.repo == "" {
		return errors.New("history requires --repo OWNER/REPO")
	}
	if *fl.limit < 0 || *fl.limit > 100 {
		return errors.New("history --limit must be between 1 and 100, or omitted for 50")
	}
	if *fl.task != "" && (*fl.historyAfter != "" || *fl.limit != 0) {
		return errors.New("history --task cannot include pagination flags")
	}
	conn, err := requireService(ctx, dir)
	if err != nil {
		return err
	}
	body := map[string]any{"town": *fl.repo}
	if *fl.task != "" {
		body["task"] = *fl.task
		var result town.Task
		if err := request(ctx, conn, "POST", "/api/history", body, &result); err != nil {
			return err
		}
		if *fl.json {
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		fmt.Printf("%s · %s · %s\n%s\n%s\n", result.ID, result.Stage, result.Updated.Format("2006-01-02"), result.Title, result.Detail)
		if result.MayoralDecision != "" {
			fmt.Println("Mayor decision:", result.MayoralDecision)
		}
		if result.Audit != nil {
			fmt.Println(result.Audit.Summary)
		}
		return nil
	}
	body["after"], body["limit"] = *fl.historyAfter, *fl.limit
	var page town.HistoryPage
	if err := request(ctx, conn, "POST", "/api/history", body, &page); err != nil {
		return err
	}
	if *fl.json {
		data, _ := json.MarshalIndent(page, "", "  ")
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("%s · %d archived tasks · active history retained for %d days\n", page.Town, page.Total, page.RetentionDays)
	for _, item := range page.Items {
		fmt.Printf("%s  %s  %s  %s\n", item.ID, item.Stage, item.Updated.Format("2006-01-02"), item.Title)
	}
	if page.Next != "" {
		fmt.Printf("Next page: --after %q\n", page.Next)
	}
	return nil
}
