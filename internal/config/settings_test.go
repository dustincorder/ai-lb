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
