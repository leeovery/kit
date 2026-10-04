package steps_test

import (
	"context"
	"testing"

	"github.com/leeovery/kit/internal/check"
	"github.com/leeovery/kit/internal/runner"
	"github.com/leeovery/kit/internal/runner/runnertest"
	"github.com/leeovery/kit/internal/steps"
)

const dir = "/Users/someone/.config/kit"

func TestConfigPrivate(t *testing.T) {
	remote := []string{"-C", dir, "remote", "get-url", "origin"}
	view := func(repo string) []string {
		return []string{"repo", "view", repo, "--json", "visibility", "--jq", ".visibility"}
	}
	tests := []struct {
		name   string
		script func(*runnertest.Fake)
		want   check.Result
	}{
		{
			name: "private, over SSH",
			script: func(f *runnertest.Fake) {
				f.On("git", remote...).Prints("git@github.com:someone/kit-config.git\n")
				f.On("gh", view("someone/kit-config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.OK, Summary: "private on GitHub (someone/kit-config)"},
		},
		{
			name: "private, over HTTPS",
			script: func(f *runnertest.Fake) {
				f.On("git", remote...).Prints("https://github.com/someone/my.config\n")
				f.On("gh", view("someone/my.config")...).Prints("PRIVATE\n")
			},
			want: check.Result{State: check.OK, Summary: "private on GitHub (someone/my.config)"},
		},
		{
			name: "public",
			script: func(f *runnertest.Fake) {
				f.On("git", remote...).Prints("ssh://git@github.com/someone/kit-config.git\n")
				f.On("gh", view("someone/kit-config")...).Prints("PUBLIC\n")
			},
			want: check.Result{State: check.Attention, Summary: "public on GitHub (someone/kit-config): it must be private", Items: []check.Item{{
				ID: "config-private:someone/kit-config", Name: "someone/kit-config", State: "not-private",
				Detail: "make it private: gh repo edit someone/kit-config --visibility private --accept-visibility-change-consequences",
			}}},
		},
		{
			name: "no remote",
			script: func(f *runnertest.Fake) {
				f.On("git", remote...).Exits(2).PrintsToStderr("error: No such remote 'origin'")
			},
			want: check.Result{State: check.OK, Summary: "no remote, so not public"},
		},
		{
			name: "elsewhere",
			script: func(f *runnertest.Fake) {
				f.On("git", remote...).Prints("git@git.example.com:someone/kit-config.git\n")
			},
			want: check.Result{State: check.OK, Summary: "not on GitHub, so not public there"},
		},
		{
			name: "GitHub can't be asked",
			script: func(f *runnertest.Fake) {
				f.On("git", remote...).Prints("git@github.com:someone/kit-config.git\n")
				f.On("gh", view("someone/kit-config")...).Exits(4).PrintsToStderr("To get started with GitHub CLI, please run:  gh auth login")
			},
			want: check.Result{State: check.Failed, Reason: "couldn't ask GitHub: gh repo view someone/kit-config --json visibility --jq .visibility exited 4: To get started with GitHub CLI, please run:  gh auth login"},
		},
		{
			name:   "no git",
			script: func(f *runnertest.Fake) { f.On("git", remote...).Fails(runner.ErrNotFound) },
			want:   check.Result{State: check.Failed, Reason: "not found"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := runnertest.New(t)
			tt.script(fake)
			s := steps.ConfigPrivate(fake, dir)
			got := s.Check(context.Background())
			if got.State != tt.want.State || got.Summary != tt.want.Summary || got.Reason != tt.want.Reason || len(got.Items) != len(tt.want.Items) {
				t.Fatalf("check = %+v\nwant %+v", got, tt.want)
			}
			for i := range got.Items {
				if got.Items[i] != tt.want.Items[i] {
					t.Errorf("item = %+v, want %+v", got.Items[i], tt.want.Items[i])
				}
			}
		})
	}
}
