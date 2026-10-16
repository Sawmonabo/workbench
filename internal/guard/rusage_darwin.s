#include "textflag.h"

TEXT libc_proc_pid_rusage_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_proc_pid_rusage(SB)
GLOBL	·procPidRusage(SB), RODATA, $8
DATA	·procPidRusage(SB)/8, $libc_proc_pid_rusage_trampoline<>(SB)
