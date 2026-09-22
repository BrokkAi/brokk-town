package town

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// simplifierIntake is one arrival sitting in Simplifier intake, the state every
// assessment is dispatched from.
func simplifierIntake(kind string, number int) (*State, *Town, *Task) {
	state := NewState(false)
	town, _ := state.Add(DefaultConfig("acme/orchard"))
	task := &Task{
		ID: fmt.Sprintf("%s:%d", kind, number), Kind: kind, Number: number, Title: "Arrival",
		Stage: "simplifying", House: Simplifier, Updated: time.Now(),
	}
	town.Tasks[task.ID] = task
	return &state, town, task
}

func TestStaleSimplifierResultCannotResurrectAClosedPR(t *testing.T) {
	state, town, task := simplifierIntake("pr", 5)
	// Repo Bot observed the closure while Simplifier was still running.
	task.Stage = "closed"
	admit := &Simplification{Mode: "auto", Decision: "admit", Detail: "Focused change."}
	applySimplification(state, town, task, admit, nil, time.Now())
	if task.Stage != "closed" {
		t.Fatalf("a confirmed closure was overwritten: stage=%s house=%s", task.Stage, task.House)
	}
	if task.House == Review {
		t.Fatal("a closed pull request was handed to Review")
	}
	if task.Simplification != nil {
		t.Fatal("a discarded assessment was still attached")
	}
	found := false
	for _, e := range state.Events {
		if strings.Contains(e.Title, "discarded") {
			found = true
		}
	}
	if !found {
		t.Fatal("discarding the result recorded no event")
	}
}

func TestStaleSimplifierResultCannotOverrideAMayoralDecision(t *testing.T) {
	state, town, task := simplifierIntake("issue", 7)
	// The Mayor decided this arrival while the assessment was in flight.
	task.Stage, task.House, task.MayoralDecision = "declined", Hall, "declined"
	applySimplification(state, town, task, &Simplification{Mode: "auto", Decision: "admit", Detail: "Worth doing."}, nil, time.Now())
	if task.Stage != "declined" || task.House != Hall || task.MayoralDecision != "declined" {
		t.Fatalf("a Mayoral decision was overwritten: %+v", task)
	}
}

func TestStaleSimplifierFailureDoesNotDelayWorkThatLeftIntake(t *testing.T) {
	state, town, task := simplifierIntake("issue", 9)
	task.Stage, task.House = "queued", Issue
	applySimplification(state, town, task, nil, errors.New("agent exited"), time.Now())
	if task.Attempts != 0 || !task.RetryAt.IsZero() || task.Blocked {
		t.Fatalf("a task that left intake was penalised: %+v", task)
	}
	if task.Stage != "queued" || task.House != Issue {
		t.Fatalf("a task that left intake was moved: %s/%s", task.House, task.Stage)
	}
}

func TestSimplifierResultStillAppliesToWorkStillInIntake(t *testing.T) {
	state, town, task := simplifierIntake("pr", 3)
	admit := &Simplification{Mode: "auto", Decision: "admit", Detail: "Focused change."}
	applySimplification(state, town, task, admit, nil, time.Now())
	if task.Stage != "queued" || task.House != Review || task.Simplification != admit {
		t.Fatalf("an in-intake assessment was refused: %+v", task)
	}
	// A cancellation still leaves intake untouched for the next dispatch.
	state2, town2, task2 := simplifierIntake("issue", 4)
	applySimplification(state2, town2, task2, nil, context.Canceled, time.Now())
	if task2.Attempts != 0 || task2.Stage != "simplifying" {
		t.Fatalf("cancellation disturbed intake: %+v", task2)
	}
}
