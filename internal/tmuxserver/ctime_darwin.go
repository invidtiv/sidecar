//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package tmuxserver

import "syscall"

func ctimeNano(st *syscall.Stat_t) int64 {
	return st.Ctimespec.Nano()
}
