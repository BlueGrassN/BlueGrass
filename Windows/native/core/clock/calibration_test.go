package clock

import (
	"testing"
	"time"
)

func TestFirstServerCalibrationReplacesLocalEstimateAndNeverRewindsAfterward(t *testing.T) {
	c := New("runtime", time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC), Local)
	server := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	if err := c.Calibrate(server); err != nil {
		t.Fatal(err)
	}
	first := c.Read()
	if first.Confidence != Server || first.EffectiveUTC.Sub(server) > time.Second {
		t.Fatal("local clock was not replaced")
	}
	if err := c.Calibrate(server.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c.Read().EffectiveUTC.Before(first.EffectiveUTC) {
		t.Fatal("old server response extended expiry")
	}
	if c.Calibrate(time.Time{}) == nil {
		t.Fatal("accepted missing server anchor")
	}
}
