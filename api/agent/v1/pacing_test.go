package v1

import "testing"

func TestPacingBounds(t *testing.T) {
	if e := DefaultPacing().Validate(); e != nil {
		t.Fatal(e)
	}
	for name, mutate := range map[string]func(*Pacing){
		"heartbeat too short": func(p *Pacing) { p.HeartbeatSeconds = MinHeartbeatSeconds - 1 },
		"heartbeat too long":  func(p *Pacing) { p.HeartbeatSeconds = MaxHeartbeatSeconds + 1 },
		"negative jitter":     func(p *Pacing) { p.JitterPercent = -1 },
		"too much jitter":     func(p *Pacing) { p.JitterPercent = MaxJitterPercent + 1 },
		"no reconnect delay":  func(p *Pacing) { p.ReconnectBaseSeconds = 0 },
		"base too long": func(p *Pacing) {
			p.ReconnectBaseSeconds, p.ReconnectMaxSeconds = MaxReconnectBaseSeconds+1, MaxReconnectMaxSeconds
		},
		"maximum below base":   func(p *Pacing) { p.ReconnectBaseSeconds, p.ReconnectMaxSeconds = 10, 9 },
		"maximum out of range": func(p *Pacing) { p.ReconnectMaxSeconds = MaxReconnectMaxSeconds + 1 },
	} {
		p := DefaultPacing()
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestDefaultPacingFitsEveryAllowedOnlineWindow(t *testing.T) {
	// Control refuses online_seconds below 20.
	if !DefaultPacing().FitsOnlineWindow(20) {
		t.Fatal("the default pacing would mark healthy nodes offline")
	}
}

func TestFitsOnlineWindow(t *testing.T) {
	for _, c := range []struct {
		heartbeat, jitter, online int
		fits                      bool
	}{
		{10, 10, 20, true},
		{20, 0, 30, true},
		{20, 1, 30, false},
		{120, 50, 270, true},
		{120, 50, 269, false},
	} {
		p := Pacing{HeartbeatSeconds: c.heartbeat, JitterPercent: c.jitter}
		if p.FitsOnlineWindow(c.online) != c.fits {
			t.Errorf("%+v in %d: want %v", p, c.online, c.fits)
		}
	}
}

func TestClaimWaitNeverExceedsTheShortestIntervalOrTheProtocolBound(t *testing.T) {
	for heartbeat := MinHeartbeatSeconds; heartbeat <= MaxHeartbeatSeconds; heartbeat++ {
		for jitter := 0; jitter <= MaxJitterPercent; jitter++ {
			p := Pacing{HeartbeatSeconds: heartbeat, JitterPercent: jitter}
			wait := p.ClaimWaitSeconds()
			if wait < 1 || wait > MaxClaimWaitSeconds || 100*wait > heartbeat*(100-jitter) {
				t.Fatalf("%+v waits %d", p, wait)
			}
			if wait >= p.IdleSeconds() {
				t.Fatalf("%+v: a held claim must end before the session counts as idle", p)
			}
		}
	}
}

func TestSessionIdleCoversEveryInterval(t *testing.T) {
	if got := (Pacing{HeartbeatSeconds: MinHeartbeatSeconds}).IdleSeconds(); got != SessionIdleSeconds {
		t.Fatalf("short interval idles after %d", got)
	}
	if got := (Pacing{HeartbeatSeconds: MaxHeartbeatSeconds}).IdleSeconds(); got != 3*MaxHeartbeatSeconds {
		t.Fatalf("long interval idles after %d", got)
	}
}
