package backend

import (
	"testing"
	"time"
	core "wg-turn-client"
)

func TestDisconnectWaitsForCoreCompletion(t *testing.T) {
	coreDone := make(chan struct{})
	entered := make(chan struct{})
	returned := make(chan struct{})
	b := &Bridge{core: core.New(core.Config{}), coreDone: coreDone, running: true, onEvent: func(name string, args ...any) {
		if name == "degraded" {
			close(entered)
		}
	}}
	go func() { b.Disconnect(); close(returned) }()
	<-entered
	select {
	case <-returned:
		t.Fatal("disconnect returned before old core completed")
	case <-time.After(30 * time.Millisecond):
	}
	b.mu.Lock()
	b.running = false
	b.core = nil
	b.mu.Unlock()
	close(coreDone)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not return after completion")
	}
}
