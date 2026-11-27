// Copyright (c) 2026 XDC Network

package XDPoS

import "testing"

// TestF19805CreditedAmountNotGrossSignerReward checks the amount returned for
// an address: its holder shares summed over every signer's table, also when
// the address is a signer itself, and nothing for a signer that was only
// paid out to others.
func TestF19805CreditedAmountNotGrossSignerReward(t *testing.T) {
	const a, b, owner = "0xaaaa000000000000000000000000000000000001", "0xbbbb000000000000000000000000000000000002", "0xcccc000000000000000000000000000000000003"
	body := []byte(`{
		"signers": {"` + a + `": {"Sign": 5, "Reward": 100}, "` + b + `": {"Sign": 5, "Reward": 200}},
		"rewards": {
			"` + a + `": {"` + owner + `": 70, "` + a + `": 30},
			"` + b + `": {"` + owner + `": 150, "` + a + `": 50}
		},
		"signSlots": 10
	}`)
	for _, tc := range []struct {
		acct string
		want string
	}{
		{a, "80"},      // signer and holder: 30 + 50, not the gross 100
		{b, "0"},       // signer paid out to others only: nothing credited
		{owner, "220"}, // holder in both tables
	} {
		got, err := AccountRewardFromFile(body, tc.acct)
		if err != nil {
			t.Fatal(err)
		}
		if got.String() != tc.want {
			t.Fatalf("%s: credited %s, want %s", tc.acct, got, tc.want)
		}
	}
}
