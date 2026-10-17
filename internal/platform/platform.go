// Package platform owns every operating-system call and every fact that
// differs between macOS and Linux, so no other package asks which system it
// runs on. Each subject has a _darwin.go and a _linux.go file exporting the
// same names with the same signatures; a file without a suffix holds only
// what is identical on both. The package imports only the standard library
// and golang.org/x/sys. macOS libSystem functions that x/sys does not wrap
// are called the way x/sys calls them: a cgo_import_dynamic binding and an
// assembly trampoline through the runtime's syscall.syscall6.
package platform
