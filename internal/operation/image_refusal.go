package operation

// This file holds the words of the refusals of a file or folder on disk that
// holds what an image cannot keep: each headline names the path and what it
// holds, a cause line says why that stops the write, and the fix line gives
// the command that clears the cause where one does. Attributes are named,
// never shown: no value or content reaches a message.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
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

// locked is why flags among platform.LockingFlags stop the write; what names
// them ("that flag", "those flags").
func (h holder) locked(what string) string {
	switch h {
	case replacedFile, replacedLink:
		return "no file can replace it while it has " + what
	case changedFolder:
		return "Workbench cannot change it while it has " + what
	case parentFolder:
	}
	return "no file in it can be added, replaced or removed while it has " + what
}

// lost is why any other metadata Workbench cannot keep stops the write; what
// names it ("that flag", "that attribute").
func (h holder) lost(what string) string {
	switch h {
	case replacedFile:
		return "the file Workbench writes in its place would not have " + what
	case replacedLink:
		return "the link Workbench writes in its place would not have " + what
	case changedFolder:
		return "Workbench's checkpoint of it cannot record " + what + ", so revert could not put it back"
	case parentFolder:
	}
	return "Workbench has not checked " + what + " against the files it writes there"
}

// moveAside is the fix of a file or link whose cause no command clears.
const moveAside = "move it aside"

// moveAsideFix is the fix line that moves path, a file, link or folder in
// the place Workbench needs, to a name beside it that nothing has: path with
// .moved, or .moved-2 and on after it. mv -n replaces nothing, so a name
// taken between the refusal and the move leaves path where it is, and the
// command names it again.
func moveAsideFix(path string) Part { return moveFix(path, path+".moved") }

// moveFix is the fix line that moves path to name, or, when name is taken,
// to name with -2 and on after it, the first that nothing has.
func moveFix(path, name string) Part {
	free := name
	for n := 2; ; n++ {
		if _, err := os.Lstat(free); errors.Is(err, fs.ErrNotExist) {
			break
		}
		free = fmt.Sprintf("%s-%d", name, n)
	}
	return FixText("mv -n " + shellHomePath(path) + " " + shellHomePath(free))
}

// noFix is the fix of a folder whose cause no command clears: no line.
type noFix struct{}

func (noFix) addTo(*Body) {}

// fix is the fix line of a refusal of path, which is h to the write: command,
// the command that clears its cause, changed to act on a link itself when
// path is one. A command that has no such form, or none at all, gives a file
// or link moving it aside, and a folder no line.
func (h holder) fix(command []string, path string) Part {
	if h == replacedLink {
		command = platform.NoFollow(command)
	}
	switch {
	case command != nil:
		return FixPath(path, command...)
	case h.replaced():
		return FixText(moveAside)
	}
	return noFix{}
}

// these is "that NOUN" for one, else "those NOUNs".
func these(n int, noun string) string {
	if n == 1 {
		return "that " + noun
	}
	return "those " + display.Plural(n, noun)
}

// flagRefusal refuses path for flags, the file flags on it a write cannot
// keep, named as the system's listing names them (platform.FlagNames).
func flagRefusal(path string, flags uint32, h holder) error {
	names := platform.FlagNames(flags)
	cause := h.lost(these(len(names), "flag"))
	if flags&platform.LockingFlags != 0 {
		cause = h.locked(these(len(names), "flag"))
	}
	return Fail(
		ExitBlocked,
		"metadata",
		display.Path(path)+" has the "+display.Plural(len(names), "flag")+" "+display.List(names),
		Cause(cause),
		h.fix(platform.ClearFlags(flags), path),
	)
}

// inheritedACL is why an ACL that files and folders made in a folder inherit
// stops a write into it.
const inheritedACL = "a file Workbench writes there would have an ACL"

// attributeRefusal refuses path for the extended attribute name, which a
// write cannot keep. It takes the name alone, so the attribute's value never
// reaches the message.
func attributeRefusal(path, name string, h holder) error {
	kind, remove := platform.AttributeRemoval(name)
	if kind == "" {
		kind = "the extended attribute " + shellWord(name)
	}
	held := "it"
	if !strings.HasPrefix(kind, "a ") && !strings.HasPrefix(kind, "an ") &&
		!strings.HasPrefix(kind, "the ") {
		held = "them"
	}
	cause := h.lost(held)
	if h == parentFolder && name == platform.DefaultACLAttribute {
		cause = "files and folders made in it inherit it, so " + inheritedACL
	}
	return Fail(
		ExitBlocked,
		"metadata",
		display.Path(path)+" has "+kind,
		Cause(cause),
		h.fix(remove, path),
	)
}

// attributeReadError is the refusal of path, which is h to the write, whose
// extended attributes, or the one named name when it is not empty, cannot be
// read.
func attributeReadError(path, name string, h holder, err error) error {
	what := "the extended attributes of "
	if name != "" {
		what = "the extended attribute " + shellWord(name) + " of "
	}
	return Fail(
		ExitBlocked,
		"metadata",
		"Cannot read "+what+display.Path(path),
		Cause(err.Error()),
		h.fix(nil, path),
	)
}

// ownershipRefusal refuses path, owned by the user with ID uid and of the
// status mode mode, for the first of the two things an image does not keep:
// an owner other than the current user, then the setuid, setgid or sticky bit.
func ownershipRefusal(path string, uid, mode uint32) error {
	if int(uid) != os.Geteuid() {
		return ownerRefusal(ExitBlocked, "metadata", path, uid, mode&unix.S_IFMT == unix.S_IFLNK)
	}
	return specialModeRefusal(path, mode, holderOf(mode&unix.S_IFMT))
}

// ownerRefusal refuses path, which the user with ID uid owns, not the
// current user, with code and category, and gives the chown, of the link
// itself for a link, that makes it theirs.
func ownerRefusal(code Exit, category, path string, uid uint32, link bool) error {
	command := []string{"sudo", "chown", userName(uint32(os.Geteuid()))}
	if link {
		command = platform.NoFollow(command)
	}
	return Fail(
		code,
		category,
		display.Path(path)+" belongs to "+userName(uid)+", not to you",
		Cause("Workbench writes only what you own"),
		FixPath(path, command...),
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
			"%s has the %s %s (mode %04o)",
			display.Path(path),
			display.List(bits),
			display.Plural(len(bits), "bit"),
			mode&0o7777,
		),
		Cause(h.lost(these(len(bits), "bit"))),
		h.fix([]string{"chmod", strings.Join(clear, ",")}, path),
	)
}

// linkReadRefusal refuses the link at path whose destination cannot be read,
// with err, or is longer than an image keeps, with err nil. Both end with
// moving the link aside, which needs only the right to delete it: a link whose
// ACL denies its owner reading it cannot have that ACL taken off either, since
// chmod -h reads it first.
func linkReadRefusal(path string, err error) error {
	shown := display.Path(path)
	if err != nil {
		return Fail(
			ExitBlocked,
			"image",
			"Cannot read the link "+shown,
			Cause(err.Error()),
			FixText(moveAside),
		)
	}
	return Fail(
		ExitBlocked,
		"image",
		shown+" links to a path longer than 4096 bytes",
		Cause("Workbench keeps at most 4096 bytes of a link"),
		FixText(moveAside),
	)
}

// unreachable is the cause and the fix of failing with err to look up or
// read path, a folder when folder is set. They name what keeps Workbench
// out: path itself when it can see path, otherwise the nearest folder above
// it that it can see, which then keeps it from searching it. When that is no
// folder, the fix moves it aside; when it is another user's, it gives it to
// you; when it is yours and its mode keeps you out, it gives you back what
// Workbench needs, reading a file, or reading, writing and searching a
// folder, as it writes in it; when its mode lets you in, the ACL entries
// that deny you what it needs are named and removed (see [aclKeepsOut]).
// Otherwise they are nil, and the caller's cause is err.
func unreachable(path string, folder bool, err error) []Part {
	held := path
	var stat unix.Stat_t
	for unix.Lstat(held, &stat) != nil {
		up := filepath.Dir(held)
		if up == held {
			return nil
		}
		held = up
	}
	shown := "it"
	if held != path {
		shown = display.Path(held)
	}
	format := uint32(stat.Mode & unix.S_IFMT)
	switch {
	case format != unix.S_IFDIR && (held != path || folder):
		return []Part{
			Cause(shown + " is " + kindName(format) + ", not a folder"),
			moveAsideFix(held),
		}
	case !errors.Is(err, unix.EACCES):
		return nil
	case int(stat.Uid) != os.Geteuid():
		return []Part{
			Cause(shown + " belongs to " + userName(stat.Uid) + ", not to you"),
			FixPath(held, "sudo", "chown", userName(uint32(os.Geteuid()))),
		}
	}
	needed, grant := uint32(0o100), "u+rwx"
	switch {
	case held == path && folder:
		needed = 0o500
	case held == path:
		needed, grant = 0o400, "u+r"
	}
	mode := uint32(stat.Mode) & 0o7777
	var cannot []string
	if mode&needed&0o400 != needed&0o400 {
		cannot = append(cannot, "read")
	}
	if mode&needed&0o100 != needed&0o100 {
		cannot = append(cannot, "search")
	}
	if cannot == nil {
		return aclKeepsOut(held, shown, format, needed)
	}
	return []Part{
		Cause(
			fmt.Sprintf(
				"%s has mode %04o, so you cannot %s it",
				shown,
				mode,
				strings.Join(cannot, " or "),
			),
		),
		FixPath(held, "chmod", grant),
	}
}

// aclKeepsOut is the cause and the fix of being refused needed (0o400 for
// read, 0o100 for search, or both) on held, shown in a cause as shown, of the
// type format, whose mode allows it: the ACL entries of held that deny this
// user a right it needs (platform.ReadRight, platform.SearchRight), when the
// system denies it that right (platform.Allowed). As the refusals of entries
// that deny deleting do (see [entryRefusal]), it names those that bind this
// user, or every denying entry when none resolves as binding, with one
// chmod -a# each, the highest first (see [entryFixes]). They are nil when the
// ACL keeps nothing out, or on Linux, which lists no entries.
func aclKeepsOut(held, shown string, format, needed uint32) []Part {
	acl, err := platform.FileSecurity(-1, held)
	if err != nil {
		return nil
	}
	var rights uint32
	var denied []string
	for _, right := range []struct {
		bit  uint32
		mask uint32
		verb string
	}{{0o400, platform.ReadRight, "reading"}, {0o100, platform.SearchRight, "searching"}} {
		if needed&right.bit == 0 || right.mask == 0 {
			continue
		}
		if ok, err := platform.Allowed(held, right.mask); err == nil && !ok &&
			len(denyingEntries(acl.Entries, right.mask)) > 0 {
			rights |= right.mask
			denied = append(denied, right.verb)
		}
	}
	denying := denyingEntries(acl.Entries, rights)
	if len(denying) == 0 {
		return nil
	}
	var binding []int
	for _, i := range denying {
		if acl.Binds(i) {
			binding = append(binding, i)
		}
	}
	whose := ""
	if shown != "it" {
		whose = " of " + shown
	}
	what := denyVerb(len(binding)) + " you " + strings.Join(denied, " and ") + " it"
	if len(binding) == 0 {
		binding = denying
		what = denyVerb(len(binding)) + " " + strings.Join(denied, " and ") + " it"
		if len(binding) > 1 {
			what += ", one or more of them to you"
		}
	}
	cause := "an ACL entry" + whose + " " + what
	if len(binding) > 1 {
		named := make([]string, len(binding))
		for i, entry := range binding {
			named[i] = strconv.Itoa(entry)
		}
		cause = "ACL entries " + display.List(named) + whose + " " + what
	}
	return []Part{Cause(cause), entryFixes(held, holderOf(format), binding)}
}

// unreadFailure is the failure of reading path, a folder when folder is set,
// which the system refused with err (see unreachable). It keeps the exit
// status and category of a failure Workbench does not explain.
func unreadFailure(path string, folder bool, err error) error {
	what := ""
	if folder {
		what = "the folder "
	}
	parts := unreachable(path, folder, err)
	if parts == nil {
		parts = []Part{Cause(err.Error())}
	}
	return Fail(ExitFailed, "execution", "Cannot read "+what+display.Path(path), parts...)
}

// fileShapeRefusal refuses the file at path, with links hard links to it,
// for the first of the two things an image does not keep: another hard link
// to it, then a size over MaxImageBytes.
func fileShapeRefusal(path string, links uint64) error {
	if links != 1 {
		return hardLinkRefusal(path, links)
	}
	limit := display.Limit(MaxImageBytes)
	return Fail(
		ExitBlocked,
		"image",
		display.Path(path)+" is larger than "+limit,
		Cause("Workbench keeps at most "+limit+" of a file it replaces"),
		FixText(moveAside),
	)
}

// specialFileRefusal refuses path, a file of the type format (its status
// mode's S_IFMT bits) that is no file, folder or link.
func specialFileRefusal(path string, format uint32) error {
	return Fail(
		ExitBlocked,
		"image",
		display.Path(path)+" is "+kindName(format),
		Cause("Workbench writes only files, folders and links"),
		FixText(moveAside),
	)
}

// hardLinkRefusal refuses path, a file with links hard links to it.
func hardLinkRefusal(path string, links uint64) error {
	others := int(links) - 1
	return Fail(
		ExitBlocked,
		"image",
		display.Path(path)+" has "+display.Count(others, "other hard link"),
		Cause(
			these(
				others,
				"link",
			)+" would keep the old content once Workbench writes a new file in its place",
		),
		FixText("remove the "+display.Plural(others, "other link")+", or move this one aside"),
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
	return Fail(
		ExitBlocked,
		"metadata",
		display.Path(path)+" has group "+groupName(had),
		Cause("the "+written+" Workbench writes in its place would get group "+group+
			", the group a new file in its folder gets"),
		h.fix(command, path),
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
