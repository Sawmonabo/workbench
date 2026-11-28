package platform

import "strconv"

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
