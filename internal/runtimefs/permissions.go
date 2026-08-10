package runtimefs

// PathPermissions is a read-only view of the current process's effective
// access to an existing runtime path.
type PathPermissions struct {
	Read     bool
	Write    bool
	Traverse bool
}

// InspectPathPermissions checks effective access without creating, truncating,
// chmodding, or writing a sentinel beside the target.
func InspectPathPermissions(path string) (PathPermissions, error) {
	return inspectPathPermissions(path)
}
