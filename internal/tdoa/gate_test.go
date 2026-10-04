package tdoa

import "testing"

// TestSimulatorGate is the §9.5 validation path: 5-receiver seeded
// network, 20 dB SNR, one anchor-offset receiver — recover within
// the 50 m budget and reject the corrupted pairs via outlier
// rejection.
func TestSimulatorGate(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		sc := genScenario(seed, nil)
		res, obs := solveSim(t, sc, simFs)
		if e := simFixError(t, res, sc.tx); e > 50 {
			t.Fatalf("seed %d: fix error %.1f m > 50 m budget (reason %s)",
				seed, e, res.Reason)
		}
		if !res.Accepted {
			t.Fatalf("seed %d: fix not accepted: %s", seed, res.Reason)
		}
		if len(res.Rejected) == 0 {
			t.Fatalf("seed %d: corrupted pairs not rejected", seed)
		}
		for _, oi := range res.Rejected {
			o := obs[oi]
			ids := [2]string{sc.receivers[o.I].ID, sc.receivers[o.J].ID}
			if ids[0] != sc.corruptID && ids[1] != sc.corruptID {
				t.Fatalf("seed %d: clean pair %v rejected", seed, ids)
			}
		}
	}
}

// TestSimulatorDeterminism pins seeded determinism (§9.5): identical
// seeds produce identical pair measurements.
func TestSimulatorDeterminism(t *testing.T) {
	_, obs1 := solveSim(t, genScenario(7, nil), simFs)
	_, obs2 := solveSim(t, genScenario(7, nil), simFs)
	if len(obs1) != len(obs2) {
		t.Fatalf("observation counts differ: %d vs %d", len(obs1), len(obs2))
	}
	for i := range obs1 {
		if obs1[i] != obs2[i] {
			t.Fatalf("obs %d differs: %+v vs %+v", i, obs1[i], obs2[i])
		}
	}
}

// TestSimulatorMixedRate adds the §9.5 mixed-rate tier: one receiver
// at 1.8 MSPS, the rest at 2.4, solved at the common 1.8 MSPS rate.
func TestSimulatorMixedRate(t *testing.T) {
	sc := genScenario(11, func(i int) float64 {
		if i == 2 {
			return simFsLow
		}
		return simFs
	})
	res, _ := solveSim(t, sc, simFsLow)
	if e := simFixError(t, res, sc.tx); e > 50 {
		t.Fatalf("mixed-rate fix error %.1f m > 50 m (reason %s)",
			e, res.Reason)
	}
	if !res.Accepted {
		t.Fatalf("mixed-rate fix not accepted: %s", res.Reason)
	}
}
