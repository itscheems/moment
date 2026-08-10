//go:build windows

package media

// Windows FlushFileBuffers does not accept directory handles opened through
// os.Open. A successful atomic replace remains the platform durability bound;
// authority files themselves are flushed before publication.
func syncDirectory(string) error { return nil }
