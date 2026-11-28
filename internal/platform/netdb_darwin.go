package platform

import "modernc.org/libc/honnef.co/go/netdb"

// releaseServiceTables drops the C library's netdb.Protocols and
// netdb.Services, the tables it builds at start-up for getservbyname.
func releaseServiceTables() {
	netdb.Protocols, netdb.Services = nil, nil
}
