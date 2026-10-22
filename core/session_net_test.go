package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestIsNetworkDown(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ENETUNREACH)}
	cases := []struct {
		err  error
		want bool
	}{
		{dialErr, true},
		{fmt.Errorf("TURN Allocate: %w", errors.New("turn: failed to retransmit transaction fSXz")), true},
		{errors.New("all retransmissions failed for DR56M1yNICNBgD0x"), true},
		{errors.New("Allocate error response (error 486: Allocation Quota Reached)"), false},
		{errors.New("Allocate error response (error 401: Unauthorized)"), false},
	}
	for _, c := range cases {
		if got := isNetworkDown(c.err); got != c.want {
			t.Errorf("isNetworkDown(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestDropSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	key := new(int)
	liveSessions.Store(key, cancel)
	defer liveSessions.Delete(key)
	if n := DropSessions(); n != 1 {
		t.Fatalf("DropSessions = %d, want 1", n)
	}
	if ctx.Err() == nil {
		t.Fatal("сессия не отменена")
	}
}
