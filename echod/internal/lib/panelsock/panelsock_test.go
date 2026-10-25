package panelsock

import (
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
)

// listen serves an abstract socket of the test's own, so nothing a real app holds is touched.
func listen(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("@techo5-panelsock-test-%d-%s", os.Getpid(), t.Name())
	ln, err := net.Listen("unix", name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("hi"))
			_ = c.Close()
		}
	}()
	return name
}

// A socket served under this process's own user is the app's, and is handed back as it is.
func TestTheAppsOwnSocketIsTaken(t *testing.T) {
	name := listen(t)
	c, err := Dial(name)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	b := make([]byte, 2)
	if _, err := c.Read(b); err != nil || string(b) != "hi" {
		t.Fatalf("read %q, %v", b, err)
	}
}

// A socket held by anybody else is refused, as ErrNotTheApp.
func TestAnotherUsersSocketIsRefused(t *testing.T) {
	name := listen(t)
	c, err := dialAs(name, os.Getuid()+1)
	if err == nil {
		c.Close()
		t.Fatal("a socket held by another uid was taken")
	}
	if !errors.Is(err, ErrNotTheApp) {
		t.Fatalf("err = %v, want ErrNotTheApp", err)
	}
}

// Nobody listening is the dial's own error, for the caller to try again.
func TestNoListenerIsADialError(t *testing.T) {
	_, err := Dial(fmt.Sprintf("@techo5-panelsock-test-%d-none", os.Getpid()))
	if err == nil || errors.Is(err, ErrNotTheApp) {
		t.Fatalf("err = %v, want a dial error", err)
	}
}
