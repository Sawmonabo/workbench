// Package platform owns every operating-system call and every fact that
// differs between macOS and Linux, so no other package asks which system it
// runs on.
//
// Each subject declares every exported name once, in its file without a
// suffix such as process.go, with one comment that states what callers rely
// on and how the two systems differ in what they see. Its _darwin.go and
// _linux.go files, which Go builds only for that system, declare only
// unexported names, and their comments say how that system does it: an
// exported function calls the unexported one of the same signature, an
// exported type whose fields differ by system embeds an unexported one, as
// os.File holds its *file, and a value that differs is set from an
// unexported one. What is the same on both systems has one body in the file
// without a suffix. The system is fixed when the package is built, so no
// interface stands between the two.
//
// The package imports only the standard library, golang.org/x/sys and, on
// macOS, the netdb package of the SQLite driver's C library
// (modernc.org/libc), whose tables ReleaseServiceTables lets go of. macOS
// libSystem functions that x/sys does not wrap are called the way x/sys calls
// them: a cgo_import_dynamic binding in libc_darwin.go and an assembly
// trampoline in libc_darwin.s through the runtime's syscall.syscall6.
package platform
