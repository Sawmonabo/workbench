package platform

// releaseServiceTables does nothing on Linux: the SQLite driver's C library
// builds its tables of network services only on macOS and the BSDs.
func releaseServiceTables() {}
