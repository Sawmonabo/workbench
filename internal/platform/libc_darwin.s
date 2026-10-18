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

TEXT libc_proc_pidinfo_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_proc_pidinfo(SB)
GLOBL	·procPidinfo(SB), RODATA, $8
DATA	·procPidinfo(SB)/8, $libc_proc_pidinfo_trampoline<>(SB)

TEXT libc_host_statistics64_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_host_statistics64(SB)
GLOBL	·hostStatistics64(SB), RODATA, $8
DATA	·hostStatistics64(SB)/8, $libc_host_statistics64_trampoline<>(SB)

TEXT libc_mach_host_self_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_mach_host_self(SB)
GLOBL	·machHostSelf(SB), RODATA, $8
DATA	·machHostSelf(SB)/8, $libc_mach_host_self_trampoline<>(SB)
