package match

import (
	"context"
	"errors"
	"testing"
	"time"

	"pokermind/internal/engine"
)

// alwaysFoldPlayer 永远 fold 的 mock Player。
type alwaysFoldPlayer struct{}

func (alwaysFoldPlayer) Decide(obs engine.Observation) engine.Action {
	return engine.Action{Type: engine.Fold}
}

func TestRunLive_EventSequence(t *testing.T) {
	specs := []PlayerSpec{
		{Provider: "p", Model: "A", Label: "A"},
		{Provider: "p", Model: "B", Label: "B"},
	}
	makePlayers := []func() engine.Player{
		func() engine.Player { return alwaysFoldPlayer{} },
		func() engine.Player { return alwaysFoldPlayer{} },
	}
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := make(chan LiveEvent, 256)

	done := make(chan error, 1)
	go func() {
		_, err := RunLive(ctx, specs, makePlayers, 2, cfg, 42, nil, out)
		done <- err
		close(out)
	}()

	var types []string
	for ev := range out {
		types = append(types, ev.Type)
	}

	err := <-done
	if err != nil {
		t.Fatalf("RunLive err: %v", err)
	}

	if len(types) < 4 {
		t.Fatalf("too few events: %v", types)
	}
	if types[0] != EvMatchStarted {
		t.Errorf("first event = %q want %q", types[0], EvMatchStarted)
	}
	if types[len(types)-1] != EvMatchFinished {
		t.Errorf("last event = %q want %q", types[len(types)-1], EvMatchFinished)
	}
}

func TestRunLive_CancelContext(t *testing.T) {
	specs := []PlayerSpec{
		{Provider: "p", Model: "A", Label: "A"},
		{Provider: "p", Model: "B", Label: "B"},
	}
	slow := slowPlayer{d: 2 * time.Second}
	makePlayers := []func() engine.Player{
		func() engine.Player { return slow },
		func() engine.Player { return alwaysFoldPlayer{} },
	}
	cfg := engine.Config{SmallBlind: 5, BigBlind: 10, StartingStack: 1000}

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan LiveEvent, 256)

	done := make(chan error, 1)
	go func() {
		_, err := RunLive(ctx, specs, makePlayers, 10, cfg, 1, nil, out)
		done <- err
		close(out)
	}()

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("want context.Canceled error, got nil")
		} else if !errors.Is(err, context.Canceled) {
			t.Errorf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunLive did not return after cancel within 5s")
	}
}

type slowPlayer struct{ d time.Duration }

func (s slowPlayer) Decide(obs engine.Observation) engine.Action {
	time.Sleep(s.d)
	return engine.Action{Type: engine.Fold}
}
