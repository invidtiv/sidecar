//go:build !darwin

package agentactivity

func platformForegroundProcessGroup(int) int { return 0 }
func platformForegroundArgv0s(int) []string  { return nil }
