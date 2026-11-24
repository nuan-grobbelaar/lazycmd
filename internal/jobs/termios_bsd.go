//go:build darwin || freebsd || netbsd || openbsd

package jobs

import "golang.org/x/sys/unix"

// ioctlGetTermios is the "get termios" ioctl request, which differs
// between BSD-derived kernels (incl. Darwin) and Linux.
const ioctlGetTermios = unix.TIOCGETA
