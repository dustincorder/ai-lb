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
	ControlHost string `json:"control_host"`
	ControlPort int    `json:"control_port"`
	GatewayHost string `json:"gateway_host"`
	GatewayPort int    `json:"gateway_port"`
}

// Defaults returns the default settings.
func Defaults() Settings {
	return Settings{
		ControlHost: DefaultControlHost,
		ControlPort: DefaultControlPort,
		GatewayHost: DefaultGatewayHost,
		GatewayPort: DefaultGatewayPort,
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
	return nil
}

// Addr formats host:port for net.Listen / http.Server.
func Addr(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
