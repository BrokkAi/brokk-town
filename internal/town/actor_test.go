package town

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreAsAttributesOnlyItsOwnOperatorEvents(t *testing.T) {
	store, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Update(func(s *State) error { _, e := s.Add(DefaultConfig("acme/project")); return e }); err != nil {
		t.Fatal(err)
	}
	record := func(from, title string) error {
		return store.Update(func(s *State) error {
			s.Event("acme/project", "control", from, "bug", "", title, time.Now())
			return nil
		})
	}
	var wg sync.WaitGroup
	for _, actor := range []string{"Ada (@ada)", "Grace (@grace)", ""} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.As(actor, func() error {
				if err := record("operator", "by "+actor); err != nil {
					t.Error(err)
				}
				return record("bug", "automatic during "+actor)
			})
		}()
	}
	wg.Wait()
	if err := record("operator", "outside As"); err != nil {
		t.Fatal(err)
	}
	for _, e := range store.Snapshot().Events {
		want := ""
		if e.From == "operator" && strings.HasPrefix(e.Title, "by ") {
			want = strings.TrimPrefix(e.Title, "by ")
		}
		if e.Actor != want {
			t.Errorf("%q from %s attributed to %q, want %q", e.Title, e.From, e.Actor, want)
		}
	}
}

func TestActorNamesAreBoundedAndPrintable(t *testing.T) {
	if got := clipActor("  Ada\n\x1b[31m " + strings.Repeat("é", 200)); len(got) > MaxActor || strings.ContainsAny(got, "\n\x1b") || !strings.HasPrefix(got, "Ada[31m") {
		t.Fatalf("clipped %q (%d bytes)", got, len(got))
	}
}
