package cli

import (
	"strings"
	"testing"
)

func TestRefuseUnderSudo(t *testing.T) {
	env := func(user string) func(string) string {
		return func(k string) string {
			if k == "SUDO_USER" {
				return user
			}
			return ""
		}
	}
	args := []string{"config", "team-root", "/home/me/team root.crt"}
	cases := []struct {
		name   string
		euid   int
		user   string
		refuse bool
	}{
		{"root under sudo", 0, "me", true},
		{"root without sudo", 0, "", false},
		{"root running sudo", 0, "root", false},
		{"sudo -u another user", 1001, "me", false},
		{"plain user", 1000, "", false},
	}
	for _, c := range cases {
		err := refuseUnderSudo(c.euid, env(c.user), args, false)
		if (err != nil) != c.refuse {
			t.Errorf("%s: err = %v, want refusal %v", c.name, err, c.refuse)
		}
	}
	if err := refuseUnderSudo(0, env("me"), args, true); err != nil {
		t.Errorf("help under sudo refused: %v", err)
	}
	err := refuseUnderSudo(0, env("me"), args, false)
	if want := "\n    Remedy: proximo config team-root '/home/me/team root.crt'"; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("remedy = %q, want suffix %q", err, want)
	}
}

func TestAsksForHelp(t *testing.T) {
	root := newRootCmd()
	for args, want := range map[string]bool{
		"help":                   true,
		"help trust":             true,
		"trust --help":           true,
		"config team-root -h":    true,
		"trust":                  false,
		"config team-root x.crt": false,
		"trust --typo":           false,
		"":                       false,
	} {
		if got := asksForHelp(root, strings.Fields(args)); got != want {
			t.Errorf("asksForHelp(%q) = %v, want %v", args, got, want)
		}
	}
}
