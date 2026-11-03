//go:build !darwin && !linux

package runtime

func CodexProcessSession(int) string { return "" }
