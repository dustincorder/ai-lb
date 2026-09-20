package config

import "testing"

func TestDefaults(t *testing.T) {
	s := Defaults()
	if s.ControlHost != "127.0.0.1" || s.GatewayHost != "127.0.0.1" {
		t.Errorf("defaults must be loopback-only, got %+v", s)
	}
	if s.ControlPort != 8317 || s.GatewayPort != 8318 {
		t.Errorf("unexpected default ports, got %+v", s)
	}
	if s.ControlPort <= MinUnprivilegedPort || s.GatewayPort <= MinUnprivilegedPort {
		t.Errorf("defaults must be unprivileged (>1024), got %+v", s)
	}
	if err := Validate(s); err != nil {
		t.Errorf("defaults must validate: %v", err)
	}
}

func TestValidatePort(t *testing.T) {
	for _, p := range []int{1, 1024, 8317, 65535} {
		if err := ValidatePort(p); err != nil {
			t.Errorf("port %d should be valid: %v", p, err)
		}
	}
	for _, p := range []int{0, -1, 65536, 99999} {
		if err := ValidatePort(p); err == nil {
			t.Errorf("port %d should be rejected", p)
		}
	}
}

func TestValidateHostLoopbackOnly(t *testing.T) {
	for _, h := range []string{"127.0.0.1", "127.0.0.2", "::1"} {
		if err := ValidateHost(h); err != nil {
			t.Errorf("host %q should be valid: %v", h, err)
		}
	}
	for _, h := range []string{"0.0.0.0", "192.168.1.10", "example.com", "", "::"} {
		if err := ValidateHost(h); err == nil {
			t.Errorf("host %q should be rejected", h)
		}
	}
}

func TestValidateRejectsListenerCollision(t *testing.T) {
	colliding := Settings{
		ControlHost: "127.0.0.1",
		ControlPort: 9000,
		GatewayHost: "127.0.0.1",
		GatewayPort: 9000,
		UpdateChannel: UpdateChannelStable,
	}
	if err := Validate(colliding); err == nil {
		t.Error("identical control/gateway bind addresses should be rejected")
	}
	distinct := Settings{
		ControlHost: "127.0.0.1",
		ControlPort: 9000,
		GatewayHost: "127.0.0.1",
		GatewayPort: 9001,
		UpdateChannel: UpdateChannelStable,
	}
	if err := Validate(distinct); err != nil {
		t.Errorf("distinct ports should be valid: %v", err)
	}
	// Different loopback IPs sharing a port bind independently.
	splitLoopback := Settings{
		ControlHost: "127.0.0.1",
		ControlPort: 9000,
		GatewayHost: "127.0.0.2",
		GatewayPort: 9000,
		UpdateChannel: UpdateChannelStable,
	}
	if err := Validate(splitLoopback); err != nil {
		t.Errorf("different loopback IPs on one port should be valid: %v", err)
	}
}

func TestOverridesApplyPartial(t *testing.T) {
	base := Settings{
		ControlHost: "127.0.0.1",
		ControlPort: 8401,
		GatewayHost: "127.0.0.1",
		GatewayPort: 8402,
		UpdateChannel: UpdateChannelStable,
	}
	gw := 9000
	out, err := Overrides{GatewayPort: &gw}.Apply(base)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out.ControlPort != 8401 || out.GatewayPort != 9000 {
		t.Errorf("partial override misapplied: %+v", out)
	}
	if out.ControlHost != base.ControlHost || out.GatewayHost != base.GatewayHost {
		t.Errorf("unset overrides must preserve base: %+v", out)
	}
	// Empty overrides are identity.
	same, err := Overrides{}.Apply(base)
	if err != nil || same != base {
		t.Errorf("empty overrides should be identity: %+v, %v", same, err)
	}
	// Invalid overrides fail instead of producing a bad active config.
	bad := 0
	if _, err := (Overrides{ControlPort: &bad}).Apply(base); err == nil {
		t.Error("port 0 override should be rejected")
	}
	collide := 8401
	if _, err := (Overrides{GatewayPort: &collide}).Apply(base); err == nil {
		t.Error("colliding override should be rejected")
	}
}
