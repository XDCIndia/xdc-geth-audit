// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package params

import "testing"

// TestApothemV2ConfigByRound pins the Apothem V2 config selected at each round to
// XinFinOrg/XDPoSChain v2.9.1-testnet TestnetV2Configs (PR #2702).
func TestApothemV2ConfigByRound(t *testing.T) {
	v2 := &V2{AllConfigs: ApothemV2Configs, CurrentConfig: ApothemV2Configs[0]}
	v2.BuildConfigIndex()

	type want struct {
		key                        uint64
		masternodes, prot, obs     int
		timeout                    int
		mnReward, pReward, oReward float64
	}
	cases := []struct {
		round uint64
		want  want
	}{
		{0, want{0, 15, 0, 0, 60, 0, 0, 0}},
		{899_999, want{0, 15, 0, 0, 60, 0, 0, 0}},
		{900_000, want{900_000, 108, 0, 0, 60, 0, 0, 0}},
		{15_000_000, want{900_000, 108, 0, 0, 60, 0, 0, 0}},
		{20_276_999, want{900_000, 108, 0, 0, 60, 0, 0, 0}},
		{20_277_000, want{20_277_000, 108, 0, 0, 10, 0, 0, 0}},
		{27_359_999, want{20_277_000, 108, 0, 0, 10, 0, 0, 0}},
		{27_360_000, want{27_360_000, 25, 3, 2, 10, 63.42, 50.27, 25.13}},
		{27_400_499, want{27_360_000, 25, 3, 2, 10, 63.42, 50.27, 25.13}},
		{27_400_500, want{27_400_500, 108, 432, 99999, 10, 63.4196, 50.7357, 25.3678}},
		{30_733_008, want{27_400_500, 108, 432, 99999, 10, 63.4196, 50.7357, 25.3678}},
	}
	for _, c := range cases {
		got := v2.Config(c.round)
		if got != ApothemV2Configs[c.want.key] {
			t.Errorf("round %d: selected the wrong entry, want key %d", c.round, c.want.key)
			continue
		}
		if got.MaxMasternodes != c.want.masternodes || got.MaxProtectorNodes != c.want.prot || got.MaxObserverNodes != c.want.obs {
			t.Errorf("round %d: nodes %d/%d/%d, want %d/%d/%d", c.round,
				got.MaxMasternodes, got.MaxProtectorNodes, got.MaxObserverNodes, c.want.masternodes, c.want.prot, c.want.obs)
		}
		if got.TimeoutPeriod != c.want.timeout {
			t.Errorf("round %d: TimeoutPeriod %d, want %d", c.round, got.TimeoutPeriod, c.want.timeout)
		}
		if got.MasternodeReward != c.want.mnReward || got.ProtectorReward != c.want.pReward || got.ObserverReward != c.want.oReward {
			t.Errorf("round %d: rewards %v/%v/%v, want %v/%v/%v", c.round,
				got.MasternodeReward, got.ProtectorReward, got.ObserverReward, c.want.mnReward, c.want.pReward, c.want.oReward)
		}
	}
}
