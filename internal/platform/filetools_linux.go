package platform

import "strings"

// flagTable holds the inode flags lsattr(1) names, in the order it lists them
// (e2fsprogs lib/e2p/pf.c). chattr(1) clears each by its letter, i and a only
// for root (CAP_LINUX_IMMUTABLE) and j only for root (CAP_SYS_RESOURCE); it
// may not clear e, E, I, N or V, and F only in an empty folder, so no command
// here clears those.
var flagTable = []fileFlag{
	{0x00000001, "s", "s", false},    // FS_SECRM_FL
	{0x00000002, "u", "u", false},    // FS_UNRM_FL
	{0x00000008, "S", "S", false},    // FS_SYNC_FL
	{0x00010000, "D", "D", false},    // FS_DIRSYNC_FL
	{immutableFlag, "i", "i", true},  // FS_IMMUTABLE_FL
	{appendFlag, "a", "a", true},     // FS_APPEND_FL
	{0x00000040, "d", "d", false},    // FS_NODUMP_FL
	{0x00000080, "A", "A", false},    // FS_NOATIME_FL
	{0x00000004, "c", "c", false},    // FS_COMPR_FL
	{encryptFlag, "E", "", false},    // FS_ENCRYPT_FL
	{0x00004000, "j", "j", true},     // FS_JOURNAL_DATA_FL
	{indexFlag, "I", "", false},      // FS_INDEX_FL
	{0x00008000, "t", "t", false},    // FS_NOTAIL_FL
	{0x00020000, "T", "T", false},    // FS_TOPDIR_FL
	{extentFlag, "e", "", false},     // FS_EXTENT_FL
	{0x00800000, "C", "C", false},    // FS_NOCOW_FL
	{0x02000000, "x", "x", false},    // FS_DAX_FL
	{0x40000000, "F", "", false},     // FS_CASEFOLD_FL
	{inlineDataFlag, "N", "", false}, // FS_INLINE_DATA_FL
	{0x20000000, "P", "P", false},    // FS_PROJINHERIT_FL
	{0x00100000, "V", "", false},     // FS_VERITY_FL
	{0x00000400, "m", "m", false},    // FS_NOCOMP_FL
}

// clearCommand is chattr with the letters after one minus.
func clearCommand(words []string) []string {
	return []string{"chattr", "-" + strings.Join(words, "")}
}

// lockingFlags are FS_IMMUTABLE_FL and FS_APPEND_FL: fs/namei.c refuses to
// rename over or unlink such a file (may_delete), and refuses a new entry in
// an immutable folder (inode_permission).
const lockingFlags uint32 = immutableFlag | appendFlag

// aclRemoval is setfacl -b, which removes a file's access ACL (setfacl(1)).
var aclRemoval = []string{"setfacl", "-b"}

// aclEntryRemoval is nil: fileSecurity lists no entries here.
func aclEntryRemoval(int) []string { return nil }

// noFollowOption is, by command, the option of setfattr(1), chgrp(1) and
// chown(1) under which they change a link itself.
var noFollowOption = map[string]string{
	"setfattr": "-h",
	"chgrp":    "-h",
	"chown":    "-h",
}

// attributeRemoval names the POSIX ACLs and file capabilities, which their
// own commands remove, and gives every other attribute setfattr -x, after
// sudo outside the user namespace.
func attributeRemoval(name string) (kind string, remove []string) {
	switch {
	case name == accessACLAttribute:
		return "an ACL", []string{"setfacl", "-b"}
	case name == defaultACLAttribute:
		return "a default ACL", []string{"setfacl", "-k"}
	case name == capabilityAttribute:
		return "file capabilities", []string{"sudo", "setcap", "-r"}
	case strings.HasPrefix(name, "user."):
		return "", []string{"setfattr", "-x", name}
	}
	return "", []string{"sudo", "setfattr", "-x", name}
}
