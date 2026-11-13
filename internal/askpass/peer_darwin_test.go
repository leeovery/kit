package askpass

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// Only sudo, running as root, is the real sudo: a program merely named
// sudo runs as its user, and root's other programs aren't sudo.
func TestRunsAsSudo(t *testing.T) {
	for _, tt := range []struct {
		name string
		uid  uint32
		want bool
	}{
		{"sudo", 0, true},
		{"sudo", 501, false},
		{"crontab", 0, false},
		{"sudoer", 0, false},
	} {
		var p unix.KinfoProc
		copy(p.Proc.P_comm[:], tt.name)
		p.Eproc.Ucred.Uid = tt.uid
		if got := runsAsSudo(&p); got != tt.want {
			t.Errorf("%s as %d: runsAsSudo() = %v, want %v", tt.name, tt.uid, got, tt.want)
		}
	}
	if isSudo(os.Getpid()) || isSudo(1) {
		t.Error("this process, and launchd: want neither is sudo")
	}
}
