//go:build !windows && !linux && !darwin

package devtools

// Unknown Unix variants have no audited mount-identity primitive here. Fail
// closed instead of assuming that matching device IDs imply containment.
func isMountBoundary(string) bool { return true }
