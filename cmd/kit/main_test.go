package main

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/leeovery/kit/internal/testguard"
)

func TestMain(m *testing.M) { os.Exit(testguard.Main(m)) }

// The terminal closing ends kit's run as an interrupt or a SIGTERM does:
// its context ended, kit itself not killed, which a hangup would do were it
// left to its default, so a job begun on git finishes.
func TestTheTerminalClosingEndsTheRunAsAnInterruptDoes(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		ctx, stop := ending()
		if err := syscall.Kill(os.Getpid(), sig); err != nil {
			t.Fatalf("signal %v: %v", sig, err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Errorf("%v didn't end kit's run", sig)
		}
		stop()
	}
}
