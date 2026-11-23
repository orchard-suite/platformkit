//go:build !linux

package telemetry

// rssBytes has no cheap reading off Linux; the process reports the memory
// the Go runtime holds from the system instead.
func rssBytes() (uint64, bool) { return 0, false }
