package gitshadow

import "syscall"

// syscallNoFollow refuses a symlink at the path written.
const syscallNoFollow = syscall.O_NOFOLLOW
