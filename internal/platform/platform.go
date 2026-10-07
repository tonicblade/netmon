package platform

import (
	"runtime"

	"netmon/pkg/types"
)

type Collector interface {
	Interfaces() ([]types.InterfaceInfo, error)

	InterfaceStats() ([]types.InterfaceStats, error)

	Routes() ([]types.Route, error)

	Connections() ([]types.Connection, error)
}

func New() Collector {
	return newCollector()
}

func HostInfo() types.HostInfo {
	return types.HostInfo{
		Hostname: hostname(),
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Uptime:   uptimeString(),
	}
}
