package config_test

import (
	"strings"
	"testing"

	"github.com/leeovery/kit/internal/config"
)

func TestPowerSettingsRead(t *testing.T) {
	cfg := loadRepo(t, map[string]string{"laptop/declarations": "[power settings]\n# On the charger\n-c sleep 0   # never sleep\n-a womp 1\n"})
	list, err := cfg.List("power", "laptop")
	if err != nil || len(list.Entries) != 2 || list.Entries[0].Name != "charger:sleep" || list.Entries[0].Value != "0" || list.Entries[0].Note != "never sleep" || list.Entries[1].Name != "all:womp" {
		t.Errorf("List() = %+v, %v", list.Entries, err)
	}
	if err := cfg.Declare("power", "laptop", config.Entry{Name: "battery:displaysleep", Value: "2"}, "On the charger"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, cfg, "laptop/declarations"); !strings.Contains(got, "-b displaysleep 2\n") {
		t.Errorf("laptop = %q", got)
	}
	for _, line := range []string{"-x sleep 0", "-c sleep", "sleep 0"} {
		bad := loadRepo(t, map[string]string{"laptop/declarations": "[power settings]\n" + line + "\n"})
		if _, err := bad.List("power", "laptop"); err == nil || !strings.Contains(err.Error(), "laptop/declarations:2:") {
			t.Errorf("%q: error = %v", line, err)
		}
	}
}
