package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func unixConnPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	wrap := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "test socket")
		defer f.Close()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c.(*net.UnixConn)
	}
	return wrap(fds[0]), wrap(fds[1])
}

func TestRefreshExcludesDoesNotWriteToNewDarwinSession(t *testing.T) {
	oldConn, _ := unixConnPair(t)
	newConn, _ := unixConnPair(t)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	SetExcludeSource(func() []ExcludeEntry {
		close(started)
		<-release
		return []ExcludeEntry{{Value: "192.0.2.1/32", Enabled: true}}
	})
	t.Cleanup(func() { SetExcludeSource(nil) })

	w := &WG{helperConn: oldConn}
	go func() {
		defer close(done)
		w.RefreshExcludes(func(string) {})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not start")
	}
	w.stateMu.Lock()
	w.setDarwinStateLocked(&tunHelper{conn: newConn}, nil, func(string) {})
	w.stateMu.Unlock()
	t.Cleanup(func() { close(w.refreshStop) })
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not finish")
	}
	if len(w.activeUserExcludes) != 0 {
		t.Fatalf("stale refresh modified the new session: %v", w.activeUserExcludes)
	}
}

func TestRecvTunFDClosesUnclaimedDescriptors(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		rights        int
		wantFile      bool
	}{
		{name: "helper error", message: "{\"error\":\"failed\"}\n", rights: 1},
		{name: "EOF after incomplete message", message: "{", rights: 1},
		{name: "extra rights", message: "{\"name\":\"utun-test\"}\n", rights: 2, wantFile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver, sender := unixConnPair(t)
			source, err := os.CreateTemp(t.TempDir(), "fd")
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()

			fds := make([]int, tc.rights)
			for i := range fds {
				fds[i] = int(source.Fd())
			}
			if _, _, err := sender.WriteMsgUnix([]byte(tc.message), syscall.UnixRights(fds...), nil); err != nil {
				t.Fatal(err)
			}
			if err := sender.CloseWrite(); err != nil {
				t.Fatal(err)
			}
			f, _, err := recvTunFD(receiver, func(string) {})
			if f != nil {
				f.Close()
			}
			if (f != nil) != tc.wantFile || (err == nil) != tc.wantFile {
				t.Fatalf("recvTunFD returned file=%v, err=%v", f, err)
			}

			var want syscall.Stat_t
			if err := syscall.Fstat(int(source.Fd()), &want); err != nil {
				t.Fatal(err)
			}
			for fd := 0; fd < 512; fd++ {
				if fd == int(source.Fd()) {
					continue
				}
				var found syscall.Stat_t
				if syscall.Fstat(fd, &found) == nil && found.Ino == want.Ino && found.Dev == want.Dev {
					syscall.Close(fd)
					t.Errorf("unclaimed descriptor %d was left open", fd)
				}
			}
		})
	}
}

type mockRoutes struct{ deletes int }

func (m *mockRoutes) Add(string, net.IP) error    { return syscall.EEXIST }
func (m *mockRoutes) Change(string, net.IP) error { panic("changed foreign route") }
func (m *mockRoutes) Delete(string, net.IP) error { m.deletes++; return nil }
func TestForeignRoutePreserved(t *testing.T) {
	const c = "192.0.2.0/24"
	rows := map[string]rtEntry{c: {gw: "192.0.2.7", iface: "utun7"}}
	rt := &mockRoutes{}
	g := &helperGuard{rt: rt, gw: "192.0.2.1", iface: "en0", ours: map[string]rtEntry{}, table: func() (map[string]rtEntry, error) { return rows, nil }}
	if err := g.apply("add", c); err == nil {
		t.Fatal("Cisco route reported direct")
	}
	g.cleanup()
	if len(g.ours) != 0 || rt.deletes != 0 {
		t.Fatal("foreign route claimed")
	}
	g.ours[c] = rtEntry{gw: g.gw, iface: g.iface}
	g.cleanup()
	if rt.deletes != 0 {
		t.Fatal("removed replacement route")
	}
}
func TestHelperAckAndPartialFailure(t *testing.T) {
	a, b := unixConnPair(t)
	go func() {
		var req helperRequest
		json.NewDecoder(b).Decode(&req)
		e := json.NewEncoder(b)
		e.Encode(wgHelperMsg{ID: "stale"})
		e.Encode(wgHelperMsg{ID: req.ID, Error: "192.0.2.0/24: конфликт utun7"})
	}()
	if err := helperCall(a, "rules", []string{"192.0.2.0/24"}, nil); err == nil || !strings.Contains(err.Error(), "utun7") {
		t.Fatalf("lost failure: %v", err)
	}
	b.Close()
	if err := helperCall(a, "rules", nil, nil); err == nil {
		t.Fatal("closed socket reported success")
	}
}

func TestLatestToggleWinsWithinDarwinSession(t *testing.T) {
	client, helper := unixConnPair(t)
	w := &WG{helperConn: client, darwinSession: 9}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	SetExcludeSource(func() []ExcludeEntry {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return []ExcludeEntry{{Value: "192.0.2.7", Enabled: true}}
		}
		return nil
	})
	defer SetExcludeSource(nil)
	go func() { w.refreshExcludesDarwinSession(func(string) {}, 9); close(finished) }()
	<-started
	reply := make(chan helperRequest, 1)
	go func() {
		var req helperRequest
		json.NewDecoder(helper).Decode(&req)
		reply <- req
		json.NewEncoder(helper).Encode(wgHelperMsg{ID: req.ID})
	}()
	w.refreshExcludesDarwinSession(func(string) {}, 9)
	close(release)
	<-finished
	if req := <-reply; len(req.Values) != 0 || req.Op != "rules" {
		t.Fatal(req)
	}
	if w.ruleStatus != "Действует" || len(w.activeUserExcludes) != 0 {
		t.Fatal(w.ruleStatus, w.activeUserExcludes)
	}
}

func TestHelperDeadline(t *testing.T) {
	a, _ := unixConnPair(t)
	err := helperCallWithin(a, "rules", nil, nil, 10*time.Millisecond)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

func TestRouteWatchUsesOneSnapshotForForeignHosts(t *testing.T) {
	rows := map[string]rtEntry{"0.0.0.0/1": {iface: "utun-test"}}
	hosts := map[string]int{}
	for i := range 256 {
		hosts[fmt.Sprintf("10.0.0.%d/32", i)] = 0
	}
	calls := 0
	g := &helperGuard{tunName: "utun-test", gw: "192.0.2.1", iface: "en0", rt: &mockRoutes{}, ours: map[string]rtEntry{}, foreign: hosts, table: func() (map[string]rtEntry, error) { calls++; return rows, nil }}
	g.check(g.gw, g.iface)
	if calls != 1 {
		t.Fatal(calls)
	}
	for _, missed := range hosts {
		if missed != 1 {
			t.Fatal("incorrect longest-prefix match")
		}
	}
}
