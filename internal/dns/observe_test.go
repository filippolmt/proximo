package dns

import "testing"

func TestSentinelIsUnderTheTLD(t *testing.T) {
	if got := Sentinel("test"); got != "proximo-doctor.test" {
		t.Errorf("Sentinel = %q", got)
	}
}

// Both resolver tools wrap the answer in prose, and only the address is the
// fact. A tool that answered nothing must yield nothing rather than a stray
// number from its own output.
func TestFirstIPv4ReadsBothResolverTools(t *testing.T) {
	cases := []struct {
		name, out, want string
	}{
		{
			name: "dscacheutil",
			out:  "name: proximo-doctor.test\nip_address: 127.0.0.1\n",
			want: "127.0.0.1",
		},
		{
			name: "resolvectl",
			out:  "proximo-doctor.test: 127.0.0.1\n\n-- Information acquired via protocol DNS in 1.2ms.\n",
			want: "127.0.0.1",
		},
		{
			name: "a VPN resolver answering something else",
			out:  "proximo-doctor.test: 10.20.30.40\n",
			want: "10.20.30.40",
		},
		{
			name: "nothing resolved",
			out:  "proximo-doctor.test: resolve call failed: No appropriate name servers found\n",
			want: "",
		},
		{
			name: "empty",
			out:  "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstIPv4(tc.out); got != tc.want {
				t.Errorf("firstIPv4 = %q, want %q", got, tc.want)
			}
		})
	}
}
