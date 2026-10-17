#include "textflag.h"

TEXT libc_getattrlist_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_getattrlist(SB)
GLOBL	·Getattrlist(SB), RODATA, $8
DATA	·Getattrlist(SB)/8, $libc_getattrlist_trampoline<>(SB)

TEXT libc_fgetattrlist_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_fgetattrlist(SB)
GLOBL	·Fgetattrlist(SB), RODATA, $8
DATA	·Fgetattrlist(SB)/8, $libc_fgetattrlist_trampoline<>(SB)

TEXT libc_proc_pid_rusage_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_proc_pid_rusage(SB)
GLOBL	·procPidRusage(SB), RODATA, $8
DATA	·procPidRusage(SB)/8, $libc_proc_pid_rusage_trampoline<>(SB)
