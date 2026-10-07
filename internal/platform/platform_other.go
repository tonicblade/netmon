//go:build !linux && !windows

package platform

import (
	"errors"
	"os"
	"runtime"
	"time"

	"netmon/pkg/types"
)

func newCollector() Collector { return &stubCollector{} }

type stubCollector struct{}

var errUnsupported = errors.New("native collection not implemented on " + runtime.GOOS + " yet")

func (c *stubCollector) Interfaces() ([]types.InterfaceInfo, error) { return nil, errUnsupported }
func (c *stubCollector) InterfaceStats() ([]types.InterfaceStats, error) {
	return nil, errUnsupported
}
func (c *stubCollector) Routes() ([]types.Route, error)           { return nil, errUnsupported }
func (c *stubCollector) Connections() ([]types.Connection, error) { return nil, errUnsupported }

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func uptimeString() string { return time.Duration(0).String() }
