package operation

// This file holds the words of the refusals of a file or folder on disk that
// holds what an image cannot keep: each names the path, says why in plain
// words and ends with the command that clears the cause where one does.
// Attributes are named, never shown: no value or content reaches a message.

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// holder is what a path whose metadata stops a write is to that write, which
// decides why Workbench cannot keep the metadata.
type holder int

const (
	// replacedFile is a file Workbench writes a new one in place of.
	replacedFile holder = iota
	// replacedLink is a link Workbench writes a new one in place of. The
	// commands its refusals give change the link itself (platform.NoFollow).
	replacedLink
	// changedFolder is a folder Workbench changes in place and its checkpoint
	// records, so revert can put it back: a target folder, or one that holds
	// Workbench's own files.
	changedFolder
	// parentFolder is a folder Workbench adds, replaces and removes files in.
	parentFolder
)

// holderOf is the holder of a target of the type format, its status mode's
// S_IFMT bits: changedFolder for a folder, replacedLink for a link and
// replacedFile for any other.
func holderOf(format uint32) holder {
	switch format {
	case unix.S_IFDIR:
		return changedFolder
	case unix.S_IFLNK:
		return replacedLink
	}
	return replacedFile
}

// folderOrFile is changedFolder for a folder, else replacedFile.
func folderOrFile(folder bool) holder {
	if folder {
		return changedFolder
	}
	return replacedFile
}

// replaced reports whether h is a file or link Workbench writes a new one in
// place of, which a person can move aside.
func (h holder) replaced() bool { return h == replacedFile || h == replacedLink }

// locked is why a flag among platform.LockingFlags stops the write.
func (h holder) locked() string {
	switch h {
	case replacedFile, replacedLink:
		return "under which no file can replace it"
	case changedFolder:
		return "under which Workbench cannot change it"
	case parentFolder:
	}
	return "under which no file in it can be added, replaced or removed"
}

// lost is why any other metadata Workbench cannot keep stops the write.
func (h holder) lost() string {
	switch h {
	case replacedFile:
		return "which the file Workbench writes in its place would not have"
	case replacedLink:
		return "which the link Workbench writes in its place would not have"
	case changedFolder:
		return "which Workbench's checkpoint of it cannot record, so revert could not put it back"
	case parentFolder:
	}
	return "which Workbench has not checked against the files it writes there"
}

// retry ends a refusal of path, which is h to the write, with command, the
// command that clears its cause, changed to act on a link itself when path is
// one. A command that has no such form, or none at all, ends the refusal of
// a file or link with moving it aside, and adds nothing for a folder.
func (h holder) retry(command []string, path string) string {
	if h == replacedLink {
		command = platform.NoFollow(command)
	}
	switch {
	case command != nil:
		return retry(command, path)
	case h.replaced():
		return "; move it aside and retry"
	}
	return ""
}

// retry ends a refusal of path with the command that clears its cause, given
// as words a shell reads one by one and quoted where a shell would read a
// character: "; run COMMAND PATH and retry".
func retry(command []string, path string) string {
	words := make([]string, len(command))
	for i, word := range command {
		words[i] = shellWord(word)
	}
	return "; run " + strings.Join(words, " ") + " " + shellHomePath(path) + " and retry"
}

// flagRefusal refuses path for flags, the file flags on it a write cannot
// keep, named as the system's listing names them (platform.FlagNames).
func flagRefusal(path string, flags uint32, h holder) error {
	names := platform.FlagNames(flags)
	cause := h.lost()
	if flags&platform.LockingFlags != 0 {
		cause = h.locked()
	}
	message := fmt.Sprintf(
		"%s has the %s %s, %s",
		display.Path(path),
		display.Plural(len(names), "flag"),
		display.List(names),
		cause,
	)
	return Fail(ExitBlocked, "metadata", message+h.retry(platform.ClearFlags(flags), path))
}

// inheritedACL is why an ACL that files and folders made in a folder inherit
// stops a write into it, after "that" or "which".
const inheritedACL = " files and folders made in it inherit, so a file Workbench writes there would have an ACL"

// attributeRefusal refuses path for the extended attribute name, which a
// write cannot keep. It takes the name alone, so the attribute's value never
// reaches the message.
func attributeRefusal(path, name string, h holder) error {
	kind, remove := platform.AttributeRemoval(name)
	if kind == "" {
		kind = "the extended attribute " + shellWord(name)
	}
	cause := h.lost()
	if h == parentFolder && name == platform.DefaultACLAttribute {
		cause = "which" + inheritedACL
	}
	return Fail(
		ExitBlocked,
		"metadata",
		display.Path(path)+" has "+kind+", "+cause+h.retry(remove, path),
	)
}

// attributeReadError is the refusal of path whose extended attributes, or
// the one named name when it is not empty, cannot be read.
func attributeReadError(path, name string, err error) error {
	what := "the extended attributes of "
	if name != "" {
		what = "the extended attribute " + shellWord(name) + " of "
	}
	return Fail(ExitBlocked, "metadata", "Cannot read "+what+display.Path(path)+": "+err.Error())
}

// ownershipRefusal refuses path, owned by the user with ID uid and of the
// status mode mode, for the first of the two things an image does not keep:
// an owner other than the current user, then the setuid, setgid or sticky bit.
func ownershipRefusal(path string, uid, mode uint32) error {
	if int(uid) != os.Geteuid() {
		return ownerRefusal(path, uid, mode&unix.S_IFMT == unix.S_IFLNK)
	}
	return specialModeRefusal(path, mode, holderOf(mode&unix.S_IFMT))
}

// ownerRefusal refuses path, which the user with ID uid owns, not the
// current user; a link is changed with chown -h.
func ownerRefusal(path string, uid uint32, link bool) error {
	return Fail(ExitBlocked, "metadata", notYours(path, uid, link))
}

// notYours says that path belongs to the user with ID uid, not the current
// user, and gives the chown, of the link itself for a link, that makes it
// theirs.
func notYours(path string, uid uint32, link bool) string {
	command := []string{"sudo", "chown", userName(uint32(os.Geteuid()))}
	if link {
		command = platform.NoFollow(command)
	}
	owner := display.Path(path) + " belongs to " + userName(uid)
	return owner + ", not to you, and Workbench writes only what you own" + retry(command, path)
}

// notFolderRefusal refuses path, which info describes and which is no
// folder, where Workbench keeps its own files; category is the refusal's.
func notFolderRefusal(category, path string, info os.FileInfo) error {
	return Fail(
		ExitInvalid,
		category,
		display.Path(path)+" is "+fileKind(info)+
			", not a folder, so Workbench cannot keep its files there; move it aside and retry",
	)
}

// folderModeRefusal refuses path, a folder of Workbench's own files whose
// permissions mode is not 0700.
func folderModeRefusal(path string, mode os.FileMode) error {
	return Fail(
		ExitInvalid,
		"permissions",
		fmt.Sprintf(
			"%s has mode %04o, and Workbench keeps its own files only in a folder with mode 0700",
			display.Path(path),
			uint32(mode.Perm()),
		)+retry([]string{"chmod", "700"}, path),
	)
}

// specialModeRefusal refuses path, whose mode has the setuid, setgid or
// sticky bit, which an image does not record.
func specialModeRefusal(path string, mode uint32, h holder) error {
	var bits, clear []string
	for _, bit := range []struct {
		mask        uint32
		name, clear string
	}{
		{unix.S_ISUID, "setuid", "u-s"},
		{unix.S_ISGID, "setgid", "g-s"},
		{unix.S_ISVTX, "sticky", "-t"},
	} {
		if mode&bit.mask != 0 {
			bits, clear = append(bits, bit.name), append(clear, bit.clear)
		}
	}
	return Fail(
		ExitBlocked,
		"metadata",
		fmt.Sprintf(
			"%s has the %s %s (mode %04o), %s",
			display.Path(path),
			display.List(bits),
			display.Plural(len(bits), "bit"),
			mode&0o7777,
			h.lost(),
		)+h.retry([]string{"chmod", strings.Join(clear, ",")}, path),
	)
}

// linkReadRefusal refuses the link at path whose destination cannot be read,
// with err, or is longer than an image keeps, with err nil.
func linkReadRefusal(path string, err error) error {
	if err != nil {
		return Fail(
			ExitBlocked,
			"image",
			"Cannot read the link "+display.Path(path)+": "+err.Error(),
		)
	}
	return Fail(
		ExitBlocked,
		"image",
		display.Path(path)+" links to a path longer than the 4096 bytes Workbench keeps of a link",
	)
}

// linkModeRefusal refuses the link at path whose own mode, which only macOS
// sets, is not the 0777 every link Workbench writes has.
func linkModeRefusal(path string, mode uint32) error {
	return Fail(
		ExitInvalid,
		"image",
		fmt.Sprintf(
			"%s is a link with mode %04o, and Workbench keeps a link only with mode 0777",
			display.Path(path),
			mode,
		)+replacedLink.retry([]string{"chmod", "777"}, path),
	)
}

// fileShapeRefusal refuses the file at path, with links hard links to it,
// for the first of the two things an image does not keep: another hard link
// to it, then a size over MaxImageBytes.
func fileShapeRefusal(path string, links uint64) error {
	if links != 1 {
		return hardLinkRefusal(path, links)
	}
	return Fail(
		ExitBlocked,
		"image",
		display.Path(path)+" is larger than the "+display.Limit(MaxImageBytes)+
			" Workbench keeps of a file it replaces; move it aside and retry",
	)
}

// specialFileRefusal refuses path, a file of the type format (its status
// mode's S_IFMT bits) that is no file, folder or link.
func specialFileRefusal(path string, format uint32) error {
	return Fail(
		ExitBlocked,
		"image",
		display.Path(path)+" is "+kindName(format)+
			", and Workbench writes only files, folders and links; move it aside and retry",
	)
}

// hardLinkRefusal refuses path, a file with links hard links to it.
func hardLinkRefusal(path string, links uint64) error {
	others := int(links) - 1
	return Fail(
		ExitBlocked,
		"image",
		display.Path(path)+" has "+display.Count(others, "other hard link")+
			", which would keep the old content once Workbench writes a new file in its place; remove the "+
			display.Plural(others, "other link")+" or move this one aside, and retry",
	)
}

// groupChangeRefusal refuses path, a file or a link when link, of group had,
// which native's replacement would give group gets, the group a file made in
// its folder gets. The chgrp it gives runs under sudo when the user is not a
// member of gets (chown(2)).
func groupChangeRefusal(path string, link bool, had, gets uint32) error {
	group := groupName(gets)
	command := []string{"chgrp", group}
	if permitted, err := canSetGroup(gets); err != nil || !permitted {
		command = append([]string{"sudo"}, command...)
	}
	h, written := replacedFile, "file"
	if link {
		h, written = replacedLink, "link"
	}
	found := display.Path(path) + " has group " + groupName(had)
	return Fail(
		ExitBlocked,
		"metadata",
		found+", but the "+written+" Workbench writes in its place would get group "+group+
			", the group a new file in its folder gets"+h.retry(command, path),
	)
}

// kindName names a file of the type format, its status mode's S_IFMT bits,
// as a person calls it.
func kindName(format uint32) string {
	switch format {
	case unix.S_IFREG:
		return "a file"
	case unix.S_IFDIR:
		return "a folder"
	case unix.S_IFLNK:
		return "a link"
	case unix.S_IFIFO:
		return "a named pipe (FIFO)"
	case unix.S_IFSOCK:
		return "a socket"
	case unix.S_IFCHR:
		return "a character device"
	case unix.S_IFBLK:
		return "a block device"
	}
	return "a special file"
}

// fileKind names the file info describes as [kindName] does.
func fileKind(info os.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return kindName(uint32(stat.Mode) & unix.S_IFMT)
	}
	return "a special file"
}

// imageKindName names what an image of kind holds as [kindName] does.
func imageKindName(kind ImageKind) string {
	switch kind {
	case ImageDirectory:
		return "a folder"
	case ImageSymlink:
		return "a link"
	case ImageAbsent:
		return "missing"
	case ImageFile:
	}
	return "a file"
}
