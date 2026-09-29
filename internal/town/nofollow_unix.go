//go:build unix

package town

import "syscall"

// oNoFollow refuses to open a symbolic link as the final path element.
const oNoFollow = syscall.O_NOFOLLOW
