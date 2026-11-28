package platform

import (
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// flagTable holds the chflags(2) flags fflagstostr(3) names, in the order ls
// -lO lists them. chflags(1) clears each by its keyword with "no" put before
// it, or taken off nodump's. chflags(2) lets the owner change the UF_ flags
// it lists and only the super-user SF_ARCHIVED, SF_IMMUTABLE and SF_APPEND;
// it calls UF_COMPRESSED and SF_DATALESS internal and lists neither
// SF_RESTRICTED, which System Integrity Protection keeps, nor SF_NOUNLINK or
// UF_DATAVAULT, so no command here clears those.
var flagTable = []fileFlag{
	{unix.SF_APPEND, "sappnd", "nosappnd", true},
	{unix.SF_ARCHIVED, "arch", "noarch", true},
	{unix.SF_IMMUTABLE, "schg", "noschg", true},
	{unix.SF_RESTRICTED, "restricted", "", true},
	{unix.SF_NOUNLINK, "sunlnk", "", true},
	{unix.UF_APPEND, "uappnd", "nouappnd", false},
	{unix.UF_IMMUTABLE, "uchg", "nouchg", false},
	{unix.UF_NODUMP, "nodump", "dump", false},
	{unix.UF_OPAQUE, "opaque", "noopaque", false},
	{unix.UF_HIDDEN, "hidden", "nohidden", false},
	{unix.UF_COMPRESSED, "compressed", "", false},
	{unix.UF_DATAVAULT, "datavault", "", false},
	{unix.SF_DATALESS, "dataless", "", true},
}

// clearCommand is chflags with the keywords joined by commas.
func clearCommand(words []string) []string {
	return []string{"chflags", strings.Join(words, ",")}
}

// setCommand is chflags with the keywords joined by commas.
func setCommand(words []string) []string { return clearCommand(words) }

// lockingFlags are the owner's and the system's immutable and append-only
// flags: rename(2) over such a file fails with EPERM.
const lockingFlags uint32 = unix.UF_IMMUTABLE | unix.UF_APPEND | unix.SF_IMMUTABLE | unix.SF_APPEND

// aclRemoval is chmod -N, which removes a file's ACL (chmod(1)).
var aclRemoval = []string{"chmod", "-N"}

// aclEntryRemoval is chmod -a#, which removes the ACL entry numbered entry as
// ls -le numbers them (chmod(1)).
func aclEntryRemoval(entry int) []string {
	return []string{"chmod", "-a#", strconv.Itoa(entry)}
}

// noFollowOption is, by command, the option of chflags(1), chmod(1),
// chgrp(1), chown(8) and xattr(1) under which they change a link itself.
var noFollowOption = map[string]string{
	"chflags": "-h",
	"chmod":   "-h",
	"chgrp":   "-h",
	"chown":   "-h",
	"xattr":   "-s",
}

// attributeRemoval is xattr -d for every attribute: macOS keeps ACLs apart
// from extended attributes.
func attributeRemoval(name string) (kind string, remove []string) {
	return "", []string{"xattr", "-d", name}
}
