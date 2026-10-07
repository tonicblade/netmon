// Package platform isolates every OS-specific code path behind one interface.
// Everything above this layer (collector, TUI, CLI) receives identical structs.
package platform

import (
	"runtime"

	"netmon/pkg/types"
)

// Collector is the OS abstraction. Linux reads /proc and /sys, Windows calls
// the IP Helper API. Nothing shells out.
type Collector interface {
	// Interfaces returns static info for every interface.
	Interfaces() ([]types.InterfaceInfo, error)
	// InterfaceStats returns the raw counter sample for every interface.
	InterfaceStats() ([]types.InterfaceStats, error)
	// Routes returns the routing table.
	Routes() ([]types.Route, error)
	// Connections returns active TCP/UDP sockets (best effort: PID and
	// process name may be 0/"" if not permitted).
	Connections() ([]types.Connection, error)
}

// New returns the platform collector for the current OS.
func New() Collector {
	return newCollector()
}

// HostInfo builds the dashboard header.
func HostInfo() types.HostInfo {
	return types.HostInfo{
		Hostname: hostname(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Uptime:   uptimeString(),
	}
}
