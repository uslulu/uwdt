package core

import (
	"context"
	"testing"
	"time"
)

func TestControlEventSurvivesLogFlood(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &Core{ctx: ctx, events: make(chan Event, 1)}
	c.emit(Event{Type: EventEvent, Name: "wg_config"})
	for range 100 {
		c.emit(Event{Type: EventLog})
	}
	if ev := <-c.events; ev.Name != "wg_config" {
		t.Fatal(ev)
	}
	c.emit(Event{Type: EventLog})
	done := make(chan struct{})
	go func() { c.emit(Event{Type: EventState, Status: "connected"}); close(done) }()
	<-c.events
	select {
	case ev := <-c.events:
		if ev.Status != "connected" {
			t.Fatal(ev)
		}
	case <-time.After(time.Second):
		t.Fatal("state lost")
	}
	<-done
	c.emit(Event{Type: EventLog})
	go func() { c.emit(Event{Type: EventState}); close(c.events) }()
	cancel()
	for range c.events {
	}
}
