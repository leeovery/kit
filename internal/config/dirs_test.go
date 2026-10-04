package config_test

import (
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

func TestLocate(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want config.Dirs
	}{
		{
			name: "defaults, in the home",
			want: config.Dirs{Config: "/Users/someone/.config/kit", State: "/Users/someone/.local/state/kit", Logs: "/Users/someone/Library/Logs/kit"},
		},
		{
			name: "XDG's directories",
			env:  map[string]string{"XDG_CONFIG_HOME": "/elsewhere/config", "XDG_STATE_HOME": "/elsewhere/state"},
			want: config.Dirs{Config: "/elsewhere/config/kit", State: "/elsewhere/state/kit", Logs: "/Users/someone/Library/Logs/kit"},
		},
		{
			name: "KIT_CONFIG over XDG_CONFIG_HOME",
			env:  map[string]string{"KIT_CONFIG": "/elsewhere/kit-config/", "XDG_CONFIG_HOME": "/elsewhere/config"},
			want: config.Dirs{Config: "/elsewhere/kit-config", State: "/Users/someone/.local/state/kit", Logs: "/Users/someone/Library/Logs/kit"},
		},
		{
			name: "relative XDG directories are ignored",
			env:  map[string]string{"XDG_CONFIG_HOME": "config", "XDG_STATE_HOME": "state"},
			want: config.Dirs{Config: "/Users/someone/.config/kit", State: "/Users/someone/.local/state/kit", Logs: "/Users/someone/Library/Logs/kit"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.Locate("/Users/someone", getenvOf(tt.env))
			if err != nil {
				t.Fatalf("Locate() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Locate() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLocateRefuses(t *testing.T) {
	tests := []struct {
		name string
		home string
		env  map[string]string
		want string
	}{
		{name: "no home", home: "", want: "no home directory"},
		{name: "a relative home", home: "someone", want: "no home directory"},
		{name: "a relative KIT_CONFIG", home: "/Users/someone", env: map[string]string{"KIT_CONFIG": "kit-config"}, want: `KIT_CONFIG is "kit-config": it must be an absolute path`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Locate(tt.home, getenvOf(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Locate() error = %v, want one saying %q", err, tt.want)
			}
		})
	}
}

func getenvOf(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}
