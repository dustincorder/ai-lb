// Package config holds service settings: listener addresses for the
// control and gateway servers. Values are persisted in SQLite (see
// internal/db) and editable through the management API and web UI.
package config

import (
	"fmt"
	"net"
	"strconv"
)

// Defaults: loopback-only, unprivileged ports, no root required.
const (
	DefaultControlHost  = "127.0.0.1"
	DefaultControlPort  = 8317
	DefaultGatewayHost  = "127.0.0.1"
	DefaultGatewayPort  = 8318
	MinPort             = 1
	MaxPort             = 65535
	MinUnprivilegedPort = 1024
)

// Settings is the persisted service configuration.
type Settings struct {
	ControlHost   string `json:"control_host"`
	ControlPort   int    `json:"control_port"`
	GatewayHost   string `json:"gateway_host"`
	GatewayPort   int    `json:"gateway_port"`
	UpdateChannel string `json:"update_channel"`
}

// Update channels.
const (
	UpdateChannelStable  = "stable"
	UpdateChannelNightly = "nightly"
)

// Defaults returns the default settings.
func Defaults() Settings {
	return Settings{
		ControlHost:   DefaultControlHost,
		ControlPort:   DefaultControlPort,
		GatewayHost:   DefaultGatewayHost,
		GatewayPort:   DefaultGatewayPort,
		UpdateChannel: UpdateChannelStable,
	}
}

// ValidatePort checks a user-supplied port number.
func ValidatePort(port int) error {
	if port < MinPort || port > MaxPort {
		return fmt.Errorf("port %d out of range (%d..%d)", port, MinPort, MaxPort)
	}
	return nil
}

// ValidateHost only allows loopback bind addresses. The service must not
// listen on LAN or public interfaces in the current version.
func ValidateHost(host string) error {
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("host %q is not a loopback address", host)
	}
	return nil
}

// Validate checks the whole settings record.
func Validate(s Settings) error {
	if err := ValidateHost(s.ControlHost); err != nil {
		return fmt.Errorf("control_host: %w", err)
	}
	if err := ValidatePort(s.ControlPort); err != nil {
		return fmt.Errorf("control_port: %w", err)
	}
	if err := ValidateHost(s.GatewayHost); err != nil {
		return fmt.Errorf("gateway_host: %w", err)
	}
	if err := ValidatePort(s.GatewayPort); err != nil {
		return fmt.Errorf("gateway_port: %w", err)
	}
	if sameBindAddress(s.ControlHost, s.ControlPort, s.GatewayHost, s.GatewayPort) {
		return fmt.Errorf("control and gateway listeners collide on %s",
			Addr(s.ControlHost, s.ControlPort))
	}
	if s.UpdateChannel != UpdateChannelStable && s.UpdateChannel != UpdateChannelNightly {
		return fmt.Errorf("update_channel must be %q or %q", UpdateChannelStable, UpdateChannelNightly)
	}
	return nil
}

// sameBindAddress reports whether two listener addresses would compete
// for the same socket. Different loopback IPs sharing a port are allowed
// where the OS binds them independently.
func sameBindAddress(hostA string, portA int, hostB string, portB int) bool {
	if portA != portB {
		return false
	}
	ipA := net.ParseIP(hostA)
	ipB := net.ParseIP(hostB)
	if ipA == nil || ipB == nil {
		return hostA == hostB
	}
	return ipA.Equal(ipB)
}

// Overrides carries explicitly supplied CLI flag values. Nil means "flag
// not given": the persisted setting survives for this run.
type Overrides struct {
	ControlHost *string
	ControlPort *int
	GatewayHost *string
	GatewayPort *int
}

// Apply overlays the non-nil overrides onto base without touching
// persistence. The result is validated before use.
func (o Overrides) Apply(base Settings) (Settings, error) {
	out := base
	if o.ControlHost != nil {
		out.ControlHost = *o.ControlHost
	}
	if o.ControlPort != nil {
		out.ControlPort = *o.ControlPort
	}
	if o.GatewayHost != nil {
		out.GatewayHost = *o.GatewayHost
	}
	if o.GatewayPort != nil {
		out.GatewayPort = *o.GatewayPort
	}
	if err := Validate(out); err != nil {
		return base, err
	}
	return out, nil
}

// Addr formats host:port for net.Listen / http.Server.
func Addr(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
