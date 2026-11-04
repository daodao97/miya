package delegation

// Windows does not expose a portable directory fsync through os.File.Sync.
func syncDirectory(string) error { return nil }
