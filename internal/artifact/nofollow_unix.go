//go:build unix

package artifact

import "syscall"

// NoFollow refuses to open a symbolic link as the final path element.
const NoFollow = syscall.O_NOFOLLOW
