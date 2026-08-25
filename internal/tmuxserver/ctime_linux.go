//go:build linux

package tmuxserver

import "syscall"

func ctimeNano(st *syscall.Stat_t) int64 {
	return st.Ctim.Nano()
}
