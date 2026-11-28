package platform

// ReleaseServiceTables lets go of the tables of /etc/services and
// /etc/protocols that the C library of the SQLite driver (modernc.org/libc)
// builds at start-up for getservbyname in every Workbench process on macOS,
// about 1.5 MiB that would otherwise stay live for the life of the process.
// Only a process that looks up no service or protocol by name may call it:
// getservbyname and getprotobyname then find nothing. The SQLite driver
// itself never calls either, so such a process may still open a database. On
// Linux, where that library builds no such table, it does nothing.
func ReleaseServiceTables() { releaseServiceTables() }
