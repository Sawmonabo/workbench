package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

// Bounds include both image sets. Recovery reuses these immutable images.
const (
	MaxImageBytes           = 8 << 20
	MaxCheckpointImageBytes = 32 << 20
	MaxCheckpointTargets    = 256
	MaxForwardCheckpoints   = 20
)

// Image is the exact recorded state of one target: its kind (file, directory,
// symlink or absent), mode, content or link, extended attributes and group.
type Image struct {
	Kind       string            `json:"kind"`
	Mode       uint32            `json:"mode"`
	Data       []byte            `json:"data,omitempty"`
	Link       string            `json:"link,omitempty"`
	Attributes map[string][]byte `json:"attributes,omitempty"`
	Group      *uint32           `json:"group"`
}

// TargetChange is one target's approved before and after images.
type TargetChange struct {
	Path   string `json:"path"`
	Before Image  `json:"before"`
	After  Image  `json:"after"`
}

// ImageDigest returns the SHA-256 of an image's canonical JSON.
func ImageDigest(image Image) string {
	data, _ := json.Marshal(image)
	return SHA256Hex(data)
}

func (image Image) validate(c Context, path string) error {
	if image.Mode > 0o777 || len(image.Data) > MaxImageBytes || len(image.Link) > 4096 {
		return Fail(3, "image", "Target image exceeds the supported byte or permission bounds")
	}
	if image.Kind != "absent" {
		if image.Group == nil {
			return Fail(2, "image", "Present target image requires an explicit group")
		}
	}
	for name, value := range image.Attributes {
		if !preservedAttribute(name) || len(value) > 4096 || image.Kind == "absent" {
			return Fail(3, "metadata", "Unsupported target extended attributes")
		}
	}
	switch image.Kind {
	case "absent":
		if image.Mode != 0 || len(image.Data) != 0 || image.Link != "" || image.Group != nil {
			return Fail(2, "image", "Malformed absent image")
		}
	case "file":
		if image.Link != "" {
			return Fail(2, "image", "Malformed regular file image")
		}
	case "directory":
		if len(image.Data) != 0 || image.Link != "" {
			return Fail(2, "image", "Malformed directory image")
		}
	case "symlink":
		if len(image.Data) != 0 || image.Link == "" || image.Mode != 0o777 {
			return Fail(2, "image", "Malformed symbolic link image")
		}
		target := image.Link
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		target = filepath.Clean(target)
		if target == path {
			return Fail(3, "image", "Self-referential target links are unsupported")
		}
		if err := c.ValidateTarget(target); err != nil {
			return err
		}
		if err := safeParents(target); err != nil {
			return err
		}
	default:
		return Fail(2, "image", "Unknown target image type")
	}
	return nil
}

func validateImageGroup(c Context, path string, image Image) error {
	if image.Kind == "absent" {
		return nil
	}
	if image.Group == nil {
		return Fail(2, "image", "Missing target group")
	}
	groups, err := os.Getgroups()
	if err != nil {
		return err
	}
	permitted := groupID(os.Getegid()) == *image.Group
	for _, group := range groups {
		permitted = permitted || groupID(group) == *image.Group
	}
	if !permitted {
		inherited, inheritErr := CreationGroup(c, path)
		permitted = inheritErr == nil && inherited == *image.Group
	}
	if !permitted {
		return Fail(3, "metadata", "Target group cannot be preserved by the current user")
	}
	return nil
}

// Target parents are opened component by component without following links.
// All writes use that descriptor, so a concurrent parent rename cannot redirect
// them through an escaping symbolic link.
func imageParent(c Context, path string) (*os.File, string, error) {
	if err := c.ValidateTarget(path); err != nil {
		return nil, "", err
	}
	fd, err := unix.Open(
		c.Scope.Root,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, "", err
	}
	relative, err := filepath.Rel(c.Scope.Root, filepath.Dir(path))
	if err != nil {
		_ = unix.Close(fd)
		return nil, "", err
	}
	if relative != "." {
		for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
			next, openErr := unix.Openat(
				fd,
				part,
				unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
				0,
			)
			_ = unix.Close(fd)
			if openErr != nil {
				return nil, "", openErr
			}
			fd = next
		}
	}
	if err = fileMetadata(fd, filepath.Dir(path)); err != nil {
		_ = unix.Close(fd)
		return nil, "", err
	}
	return os.NewFile(uintptr(fd), filepath.Dir(path)), filepath.Base(path), nil
}

// ReadImage never follows a target link and rejects metadata we cannot preserve.
func ReadImage(c Context, path string) (Image, error) {
	parent, name, err := imageParent(c, path)
	if errors.Is(err, unix.ENOENT) {
		return Image{Kind: "absent"}, nil
	}
	if err != nil {
		return Image{}, err
	}
	defer func() { _ = parent.Close() }()
	var stat unix.Stat_t
	if err = unix.Fstatat(
		int(parent.Fd()),
		name,
		&stat,
		unix.AT_SYMLINK_NOFOLLOW,
	); errors.Is(
		err,
		unix.ENOENT,
	) {
		return Image{Kind: "absent"}, nil
	}
	if err != nil {
		return Image{}, err
	}
	if int(stat.Uid) != os.Geteuid() || stat.Mode&0o7000 != 0 {
		return Image{}, Fail(
			3,
			"metadata",
			"Target ownership or special permissions cannot be preserved",
		)
	}
	group := uint32(stat.Gid)
	image := Image{Mode: uint32(stat.Mode) & 0o777, Group: &group}
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		data := make([]byte, 4097)
		n, readErr := unix.Readlinkat(int(parent.Fd()), name, data)
		if readErr != nil || n > 4096 {
			return Image{}, Fail(3, "image", "Cannot capture target link")
		}
		image.Kind, image.Link = "symlink", string(data[:n])
		if err = linkMetadata(path); err != nil {
			return Image{}, err
		}
		image.Attributes, err = readImageAttributes(-1, path)
		if err != nil {
			return Image{}, err
		}
	case unix.S_IFREG, unix.S_IFDIR:
		if stat.Mode&unix.S_IFMT == unix.S_IFREG && (stat.Nlink != 1 || stat.Size > MaxImageBytes) {
			return Image{}, Fail(
				3,
				"image",
				"Target has hard links or exceeds the 8 MiB image limit",
			)
		}
		fd, openErr := unix.Openat(
			int(parent.Fd()),
			name,
			unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
			0,
		)
		if openErr != nil {
			return Image{}, openErr
		}
		file := os.NewFile(uintptr(fd), path)
		defer func() { _ = file.Close() }()
		var opened unix.Stat_t
		if err = unix.Fstat(fd, &opened); err != nil {
			return Image{}, err
		}
		if opened.Ino != stat.Ino || opened.Dev != stat.Dev {
			return Image{}, Fail(4, "conflict", "Target changed during image capture")
		}
		if err = fileMetadata(fd, path); err != nil {
			return Image{}, err
		}
		image.Attributes, err = readImageAttributes(fd, path)
		if err != nil {
			return Image{}, err
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			image.Kind = "directory"
		} else {
			image.Kind = "file"
			image.Data, err = io.ReadAll(io.LimitReader(file, MaxImageBytes+1))
			if err != nil {
				return Image{}, err
			}
		}
	default:
		return Image{}, Fail(3, "image", "Special filesystem targets are not supported")
	}
	if err = image.validate(c, path); err != nil {
		return Image{}, err
	}
	return image, nil
}

func sameImage(a, b Image) bool {
	if (a.Group == nil) != (b.Group == nil) || a.Group != nil && *a.Group != *b.Group {
		return false
	}
	if a.Kind != b.Kind || a.Mode != b.Mode || a.Link != b.Link || !bytes.Equal(a.Data, b.Data) ||
		len(a.Attributes) != len(b.Attributes) {
		return false
	}
	for name, value := range a.Attributes {
		other, ok := b.Attributes[name]
		if !ok || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}

// ImageWithGroup makes group ownership explicit without granting ownership
// changes. Existing targets retain their group; new targets use native parent
// inheritance. Callers bind the completed image into their plan digest.
func ImageWithGroup(c Context, path string, image Image) (Image, error) {
	if image.Kind == "absent" {
		image.Group = nil
		return image, nil
	}
	before, err := ReadImage(c, path)
	if err != nil {
		return Image{}, err
	}
	if before.Kind != "absent" {
		image.Group = before.Group
		return image, nil
	}
	group, err := CreationGroup(c, path)
	if err != nil {
		return Image{}, err
	}
	image.Group = &group
	return image, nil
}

// CreationGroup is also the native planner's group-preservation preflight:
// an atomic native replacement must not silently change an existing group.
func CreationGroup(c Context, path string) (uint32, error) {
	if err := c.ValidateTarget(path); err != nil {
		return 0, err
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		var stat unix.Stat_t
		err := unix.Lstat(parent, &stat)
		if err == nil {
			if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
				return 0, Fail(
					3,
					"metadata",
					"Cannot establish group inheritance through a non-directory",
				)
			}
			if runtime.GOOS == "darwin" || stat.Mode&unix.S_ISGID != 0 {
				return uint32(stat.Gid), nil
			}
			return groupID(os.Getegid()), nil
		}
		if !errors.Is(err, unix.ENOENT) || parent == c.Scope.Root {
			return 0, Fail(3, "metadata", "Cannot establish target group inheritance")
		}
	}
}

func sameImageContent(a, b Image) bool {
	a.Attributes = nil
	b.Attributes = nil
	return sameImage(a, b)
}

// preservedAttribute reports whether name is a macOS extended attribute that
// images carry verbatim. Each is small, grants no access and is commonly added
// by the system: download provenance and quarantine, Finder flags and the last
// used date. Every other attribute, including ACLs, blocks planning.
func preservedAttribute(name string) bool {
	switch name {
	case "com.apple.provenance",
		"com.apple.quarantine",
		"com.apple.FinderInfo",
		"com.apple.lastuseddate#PS":
		return runtime.GOOS == "darwin"
	}
	return false
}

func readImageAttributes(fd int, path string) (map[string][]byte, error) {
	var names [4096]byte
	var n int
	var err error
	if fd < 0 {
		n, err = unix.Llistxattr(path, names[:])
	} else {
		n, err = unix.Flistxattr(fd, names[:])
	}
	if err != nil {
		return nil, Fail(3, "metadata", "Cannot inspect bounded target extended attributes")
	}
	attributes := map[string][]byte{}
	for name := range strings.SplitSeq(string(names[:n]), "\x00") {
		if name == "" {
			continue
		}
		if !preservedAttribute(name) {
			return nil, Fail(
				3,
				"metadata",
				"Target extended attributes require preservation support",
			)
		}
		var data [4096]byte
		if fd < 0 {
			n, err = unix.Lgetxattr(path, name, data[:])
		} else {
			n, err = unix.Fgetxattr(fd, name, data[:])
		}
		if err != nil {
			return nil, Fail(3, "metadata", "Cannot read bounded target extended attribute")
		}
		attributes[name] = bytes.Clone(data[:n])
	}
	return attributes, nil
}

func writeImageAttributes(fd int, path string, attributes map[string][]byte) error {
	existing, err := readImageAttributes(fd, path)
	if err != nil {
		return err
	}
	for name := range existing {
		if _, ok := attributes[name]; ok {
			continue
		}
		if fd < 0 {
			err = unix.Lremovexattr(path, name)
		} else {
			err = unix.Fremovexattr(fd, name)
		}
		if err != nil {
			return err
		}
	}
	for name, data := range attributes {
		if fd < 0 {
			err = unix.Lsetxattr(path, name, data, 0)
		} else {
			err = unix.Fsetxattr(fd, name, data, 0)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func setImageGroup(fd int, group uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if uint32(stat.Gid) == group {
		return nil
	}
	return unix.Fchown(fd, -1, int(group))
}

// Native group correction uses the opened inode for both the exact-image proof
// and chown, so replacing a path cannot redirect the approved metadata write.
func enforceNativeGroup(c Context, path string, observed Image, group uint32) error {
	if observed.Kind != "file" && observed.Kind != "directory" {
		return Fail(3, "metadata", "Native group correction requires a regular file or directory")
	}
	parent, name, err := imageParent(c, path)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	fd, err := unix.Openat(
		int(parent.Fd()),
		name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if int(stat.Uid) != os.Geteuid() || stat.Mode&0o7000 != 0 ||
		(observed.Kind == "file" && (stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1)) ||
		(observed.Kind == "directory" && stat.Mode&unix.S_IFMT != unix.S_IFDIR) {
		return Fail(5, "native_image", "Native target inode changed before group preservation")
	}
	current := Image{Kind: observed.Kind, Mode: uint32(stat.Mode) & 0o777}
	currentGroup := uint32(stat.Gid)
	current.Group = &currentGroup
	if err = fileMetadata(fd, path); err != nil {
		return err
	}
	current.Attributes, err = readImageAttributes(fd, path)
	if err != nil {
		return err
	}
	if current.Kind == "file" {
		current.Data, err = io.ReadAll(io.LimitReader(file, MaxImageBytes+1))
		if err != nil {
			return err
		}
	}
	if !sameImage(current, observed) {
		return Fail(
			5,
			"native_image",
			"Native target differs from its verified image; group was not changed",
		)
	}
	if err = setImageGroup(fd, group); err != nil {
		return err
	}
	return file.Sync()
}

// writeDirectoryImage creates name when absent, then sets the desired
// attributes, group and mode through a descriptor. It never replaces a
// non-directory.
func writeDirectoryImage(fd int, name, path string, current, desired Image) error {
	if current.Kind != "absent" && current.Kind != "directory" {
		return Fail(3, "image", "Existing directory replacement is unsupported")
	}
	if current.Kind == "absent" {
		if err := unix.Mkdirat(fd, name, desired.Mode); err != nil {
			return err
		}
	}
	directoryFD, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(directoryFD) }()
	if err = writeImageAttributes(directoryFD, path, desired.Attributes); err != nil {
		return err
	}
	if err = setImageGroup(directoryFD, *desired.Group); err != nil {
		return err
	}
	return unix.Fchmod(directoryFD, desired.Mode)
}

// stageLinkImage creates the desired symlink at temporary beside path, with
// its group and attributes, ready to replace the target.
func stageLinkImage(fd int, temporary, path string, desired Image) error {
	if err := unix.Symlinkat(desired.Link, fd, temporary); err != nil {
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, temporary, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if uint32(stat.Gid) != *desired.Group {
		group := int(*desired.Group)
		if err := unix.Fchownat(fd, temporary, -1, group, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
	}
	link := filepath.Join(filepath.Dir(path), temporary)
	return writeImageAttributes(-1, link, desired.Attributes)
}

// stageFileImage writes the desired bytes, group, mode and attributes to a new
// private file at temporary and syncs it, ready to replace the target.
func stageFileImage(fd int, temporary, path string, desired Image) error {
	flags := unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fileFD, err := unix.Openat(fd, temporary, flags, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fileFD), temporary)
	_, err = file.Write(desired.Data)
	if err == nil {
		err = setImageGroup(fileFD, *desired.Group)
	}
	if err == nil {
		err = file.Chmod(os.FileMode(desired.Mode))
	}
	if err == nil {
		err = writeImageAttributes(fileFD, path, desired.Attributes)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func writeImage(c Context, path string, expected, desired Image) error {
	if err := validateImageGroup(c, path, desired); err != nil {
		return err
	}
	current, err := ReadImage(c, path)
	if err != nil {
		return err
	}
	if !sameImage(current, expected) {
		return Fail(4, "conflict", "Target changed before its write; later edits were preserved")
	}
	if sameImage(current, desired) {
		return nil
	}
	parent, name, err := imageParent(c, path)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	fd := int(parent.Fd())
	if desired.Kind == "absent" {
		flags := 0
		if current.Kind == "directory" {
			flags = unix.AT_REMOVEDIR
		}
		if err = unix.Unlinkat(fd, name, flags); err != nil {
			return err
		}
		return parent.Sync()
	}
	if desired.Kind == "directory" {
		if err = writeDirectoryImage(fd, name, path, current, desired); err != nil {
			return err
		}
		return parent.Sync()
	}
	if current.Kind == "directory" {
		return Fail(3, "image", "Directory replacement is unsupported")
	}
	id, err := NewID()
	if err != nil {
		return err
	}
	temporary := ".workbench-" + id
	defer func() { _ = unix.Unlinkat(fd, temporary, 0) }()
	stage := stageFileImage
	if desired.Kind == "symlink" {
		stage = stageLinkImage
	}
	if err = stage(fd, temporary, path, desired); err != nil {
		return err
	}
	// A final check narrows races with non-Workbench writers; conflicts never
	// trigger a rollback that could destroy their newly written data.
	current, err = ReadImage(c, path)
	if err != nil {
		return err
	}
	if !sameImage(current, expected) {
		return Fail(4, "conflict", "Target changed while preparing its replacement")
	}
	if err = replaceImage(fd, temporary, name, expected.Kind == "absent"); err != nil {
		return err
	}
	return parent.Sync()
}

// groupID converts a group ID from the os package, which uses int. Group IDs
// are 32-bit on macOS and Linux, so the conversion is exact.
func groupID(id int) uint32 { return uint32(id) }
