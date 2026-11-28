package platform

import (
	"slices"
	"strconv"
)

// fileFlag is one file flag as this system's own tools name it: name as its
// listing shows it, clear the word that clears it in its command, empty when
// that command cannot, and root whether only root may.
type fileFlag struct {
	bit         uint32
	name, clear string
	root        bool
}

// FlagNames are the names of the file flags in flags, in the order this
// system's listing gives them: ls -lO on macOS (uchg, hidden), lsattr on
// Linux, one letter each (i, A). A flag the listing does not name follows as
// its value in hexadecimal (0x40).
func FlagNames(flags uint32) []string {
	var names []string
	for _, flag := range flagTable {
		if flags&flag.bit != 0 {
			names = append(names, flag.name)
			flags &^= flag.bit
		}
	}
	for bit := uint32(1); bit != 0; bit <<= 1 {
		if flags&bit != 0 {
			names = append(names, "0x"+strconv.FormatUint(uint64(bit), 16))
		}
	}
	return names
}

// ClearFlags is the command, as words a shell reads one by one and without
// the file's path, that clears flags from a file its user owns: on macOS
// chflags with each flag's opposite keyword (chflags nouchg,dump), on Linux
// chattr with each flag's letter after one minus (chattr -iA), both after
// sudo when one of the flags only root may clear. It is nil when one of them
// has no such command: on macOS a flag chflags(2) calls internal or does not
// list, on Linux one chattr(1) may not clear, and on both a flag the listing
// does not name.
func ClearFlags(flags uint32) []string {
	var words []string
	root := false
	for _, flag := range flagTable {
		if flags&flag.bit == 0 {
			continue
		}
		if flag.clear == "" {
			return nil
		}
		words = append(words, flag.clear)
		root = root || flag.root
		flags &^= flag.bit
	}
	if flags != 0 || len(words) == 0 {
		return nil
	}
	command := clearCommand(words)
	if root {
		command = append([]string{"sudo"}, command...)
	}
	return command
}

// LockingFlags are the file flags under which a file can be neither renamed
// over nor removed, so no file replaces it, and under which nothing in a
// folder can be added, renamed or removed: immutable and append-only, on
// macOS both the owner's (uchg, uappnd) and the system's (schg, sappnd).
const LockingFlags = lockingFlags

// AttributeRemoval says how a person removes the extended attribute name
// from a file. kind is what the attribute is when that is more than its
// name: on Linux "an ACL", "a default ACL" or "file capabilities", and empty
// for every other attribute and on macOS. remove is the command, as words a
// shell reads one by one and without the file's path, that removes it:
// xattr -d NAME on macOS; on Linux setfacl -b for an ACL, setfacl -k for a
// default ACL, sudo setcap -r for file capabilities, and setfattr -x NAME for
// any other attribute, after sudo outside the user namespace, whose
// attributes alone a file's owner may remove (xattr(7)).
func AttributeRemoval(name string) (kind string, remove []string) {
	return attributeRemoval(name)
}

// ACLRemoval is the command, as words without the file's path, that removes
// a file's whole ACL: chmod -N on macOS, setfacl -b on Linux.
func ACLRemoval() []string { return slices.Clone(aclRemoval) }

// ACLEntryRemoval is the command, as words without the file's path, that
// removes the ACL entry FileSecurity lists at index entry, which is its number
// in ls -le: chmod -a# ENTRY on macOS. It is nil on Linux, where FileSecurity
// lists no entries.
func ACLEntryRemoval(entry int) []string { return aclEntryRemoval(entry) }

// NoFollow is command, as words without the file's path, given the option
// under which it changes a link itself instead of the file the link points
// to, put after its name (and after a sudo before it): -h for chflags, chmod,
// chgrp and chown and -s for xattr on macOS; -h for setfattr, chgrp and chown
// on Linux. It is nil for a command without such an option: on Linux chattr,
// which cannot open a link, and setfacl and setcap, since Linux keeps no ACL
// or file capabilities on a link.
func NoFollow(command []string) []string {
	at := 0
	if len(command) > 0 && command[0] == "sudo" {
		at = 1
	}
	if at >= len(command) {
		return nil
	}
	option, ok := noFollowOption[command[at]]
	if !ok {
		return nil
	}
	return append(append(slices.Clone(command[:at+1]), option), command[at+1:]...)
}
