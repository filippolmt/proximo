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
		err := refuseUnderSudo(c.euid, env(c.user), args)
		if (err != nil) != c.refuse {
			t.Errorf("%s: err = %v, want refusal %v", c.name, err, c.refuse)
		}
	}
	err := refuseUnderSudo(0, env("me"), args)
	if want := "\n  proximo config team-root '/home/me/team root.crt'"; err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("remedy = %q, want suffix %q", err, want)
	}
}
