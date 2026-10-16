#include "textflag.h"

TEXT libc_getattrlist_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_getattrlist(SB)
GLOBL	·getattrlist(SB), RODATA, $8
DATA	·getattrlist(SB)/8, $libc_getattrlist_trampoline<>(SB)

TEXT libc_fgetattrlist_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_fgetattrlist(SB)
GLOBL	·fgetattrlist(SB), RODATA, $8
DATA	·fgetattrlist(SB)/8, $libc_fgetattrlist_trampoline<>(SB)

TEXT libc_proc_pid_rusage_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_proc_pid_rusage(SB)
GLOBL	·procPidRusage(SB), RODATA, $8
DATA	·procPidRusage(SB)/8, $libc_proc_pid_rusage_trampoline<>(SB)
