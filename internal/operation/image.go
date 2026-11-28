package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/platform"
	"golang.org/x/sys/unix"
)

// Bounds include both image sets. Recovery reuses these immutable images.
const (
	MaxImageBytes           = 8 << 20
	MaxCheckpointImageBytes = 32 << 20
	MaxCheckpointTargets    = 256
	MaxForwardCheckpoints   = 20
)

// ImageKind is what a target is, or [ImageAbsent] when it does not exist.
type ImageKind string

// Image kinds.
const (
	ImageAbsent    ImageKind = "absent"
	ImageFile      ImageKind = "file"
	ImageDirectory ImageKind = "directory"
	ImageSymlink   ImageKind = "symlink"
)

// Image is the exact recorded state of one target: its kind, mode, content or
// link, extended attributes, ACL, hidden flag and group. ACL is the ACL as
// the system keeps it apart from the attributes (platform.FileSecurity), nil
// for none and on a system whose ACLs are extended attributes. Hidden is the
// file flag platform.HiddenFlag, which only hides a file.
type Image struct {
	Kind       ImageKind         `json:"kind"`
	Mode       uint32            `json:"mode"`
	Data       []byte            `json:"data,omitempty"`
	Link       string            `json:"link,omitempty"`
	Attributes map[string][]byte `json:"attributes,omitempty"`
	ACL        []byte            `json:"acl,omitempty"`
	Hidden     bool              `json:"hidden,omitempty"`
	Group      *uint32           `json:"group"`
}

// Carried is after, the image Workbench writes in place of before, with
// before's extended attributes, ACL and hidden flag when both are the same
// kind of file, folder or link, so what Workbench writes keeps them and
// revert puts back what it had. An access ACL that is an attribute is the one
// after's mode gives it (platform.ACLForMode), which a write that sets the
// ACL and then the mode leaves on disk.
func Carried(before, after Image) Image {
	if before.Kind == ImageAbsent || before.Kind != after.Kind {
		return after
	}
	after.Attributes = maps.Clone(before.Attributes)
	if acl, ok := after.Attributes[platform.AccessACLAttribute]; ok &&
		platform.AccessACLAttribute != "" {
		after.Attributes[platform.AccessACLAttribute] = platform.ACLForMode(acl, after.Mode)
	}
	after.ACL = slices.Clone(before.ACL)
	after.Hidden = before.Hidden
	return after
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
	shown := display.Path(path)
	switch {
	case image.Mode&^0o777 != 0 && (image.Kind != ImageDirectory || image.Mode&^0o777 != unix.S_ISGID):
		return Fail(
			ExitBlocked,
			"image",
			fmt.Sprintf("An image of %s has mode %04o", shown, image.Mode),
			Cause("an image keeps a mode up to 0777"),
		)
	case len(image.Data) > MaxImageBytes:
		return Fail(
			ExitBlocked,
			"image",
			"An image of "+shown+" holds more than "+display.Limit(MaxImageBytes),
			Cause("an image keeps at most "+display.Limit(MaxImageBytes)+" of a file"),
		)
	case len(image.Link) > 4096:
		return Fail(
			ExitBlocked,
			"image",
			"An image of "+shown+" links to a path longer than 4096 bytes",
			Cause("an image keeps at most 4096 bytes of a link"),
		)
	}
	if image.Kind != ImageAbsent {
		if image.Group == nil {
			return Fail(ExitInvalid, "image", "An image of "+shown+" has no group")
		}
	}
	if err := image.validateCarried(shown); err != nil {
		return err
	}
	switch image.Kind {
	case ImageAbsent:
		if image.Mode != 0 || len(image.Data) != 0 || image.Link != "" || image.Group != nil {
			return Fail(ExitInvalid, "image", "Malformed absent image of "+shown)
		}
	case ImageFile:
		if image.Link != "" {
			return Fail(ExitInvalid, "image", "Malformed regular file image of "+shown)
		}
	case ImageDirectory:
		if len(image.Data) != 0 || image.Link != "" {
			return Fail(ExitInvalid, "image", "Malformed directory image of "+shown)
		}
	case ImageSymlink:
		if len(image.Data) != 0 || image.Link == "" {
			return Fail(ExitInvalid, "image", "Malformed symbolic link image of "+shown)
		}
		target := image.Link
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		target = filepath.Clean(target)
		if target == path {
			return Fail(
				ExitBlocked,
				"image",
				shown+" is a link to itself",
				Cause("Workbench does not keep such a link"),
				FixPath(path, "rm"),
			)
		}
		if err := c.validateLinkTarget(path, target); err != nil {
			return err
		}
	default:
		return Fail(ExitInvalid, "image", "Unknown image type for "+shown)
	}
	return nil
}

// validateCarried refuses the extended attributes, ACL and hidden flag of
// an image of what shown names that an image does not keep.
func (image Image) validateCarried(shown string) error {
	if image.Kind == ImageAbsent && len(image.Attributes) > 0 {
		return Fail(
			ExitBlocked,
			"metadata",
			"An image of "+shown+" holds extended attributes",
			Cause("it is of a file that does not exist"),
		)
	}
	for _, name := range slices.Sorted(maps.Keys(image.Attributes)) {
		held := "An image of " + shown + " holds the extended attribute " + shellWord(name)
		switch {
		case !carriedAttribute(name):
			return Fail(ExitBlocked, "metadata", held, Cause("images do not carry it"))
		case len(image.Attributes[name]) > 4096:
			return Fail(
				ExitBlocked,
				"metadata",
				held+" larger than 4 KiB",
				Cause("an image keeps at most 4 KiB of an attribute"),
			)
		}
	}
	switch {
	case image.Kind == ImageAbsent && (image.ACL != nil || image.Hidden):
		return Fail(
			ExitBlocked,
			"metadata",
			"An image of "+shown+" holds an ACL or the hidden flag",
			Cause("it is of a file that does not exist"),
		)
	case image.ACL != nil && platform.ACLsAreAttributes:
		return Fail(
			ExitInvalid,
			"image",
			"An image of "+shown+" holds an ACL apart from its extended attributes",
			Cause("extended attributes keep ACLs here"),
		)
	case len(image.ACL) > 4096:
		return Fail(
			ExitBlocked,
			"metadata",
			"An image of "+shown+" holds an ACL larger than 4 KiB",
			Cause("an image keeps at most 4 KiB of an ACL"),
		)
	case image.Hidden && platform.HiddenFlag == 0:
		return Fail(
			ExitInvalid,
			"image",
			"An image of "+shown+" holds the hidden flag",
			Cause("files here do not have it"),
		)
	}
	return nil
}

// Target parents are opened component by component without following links.
// All writes use that descriptor, so a concurrent parent rename cannot redirect
// them through an escaping symbolic link.
func imageParent(c Context, path string) (*os.File, string, error) {
	if err := c.validateImagePath(path); err != nil {
		return nil, "", err
	}
	folder := c.Scope.Root
	fd, err := unix.Open(
		folder,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return nil, "", folderOpenError(folder, err)
	}
	relative, err := filepath.Rel(c.Scope.Root, filepath.Dir(path))
	if err != nil {
		_ = unix.Close(fd)
		return nil, "", err
	}
	if relative != "." {
		for part := range strings.SplitSeq(relative, string(filepath.Separator)) {
			folder = filepath.Join(folder, part)
			next, openErr := unix.Openat(
				fd,
				part,
				unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
				0,
			)
			_ = unix.Close(fd)
			if openErr != nil {
				return nil, "", folderOpenError(folder, openErr)
			}
			fd = next
		}
	}
	if err = parentMetadata(fd, filepath.Dir(path)); err != nil {
		_ = unix.Close(fd)
		return nil, "", err
	}
	return os.NewFile(uintptr(fd), filepath.Dir(path)), filepath.Base(path), nil
}

// folderOpenError is err, which opening folder on the way to a target
// returned: as it is when folder does not exist, which [ReadImage] reads as
// an absent target, otherwise as [unreadFailure] says.
func folderOpenError(folder string, err error) error {
	if errors.Is(err, unix.ENOENT) {
		return err
	}
	return unreadFailure(folder, true, err)
}

// ReadImage never follows a target link and rejects metadata we cannot preserve.
func ReadImage(c Context, path string) (Image, error) {
	parent, name, err := imageParent(c, path)
	if errors.Is(err, unix.ENOENT) {
		return Image{Kind: ImageAbsent}, nil
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
		return Image{Kind: ImageAbsent}, nil
	}
	if err != nil {
		return Image{}, unreadFailure(path, false, err)
	}
	if int(stat.Uid) != os.Geteuid() || specialBits(uint32(stat.Mode)) != 0 {
		return Image{}, ownershipRefusal(path, stat.Uid, uint32(stat.Mode))
	}
	group := uint32(stat.Gid)
	image := Image{Mode: imageMode(uint32(stat.Mode)), Group: &group}
	var held security
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		data := make([]byte, 4097)
		n, readErr := unix.Readlinkat(int(parent.Fd()), name, data)
		if readErr != nil || n > 4096 {
			return Image{}, linkReadRefusal(path, readErr)
		}
		image.Kind, image.Link = ImageSymlink, string(data[:n])
		if held, err = linkMetadata(path); err != nil {
			return Image{}, err
		}
		image.Attributes, err = readImageAttributes(atPath(path), path, replacedLink)
		if err != nil {
			return Image{}, err
		}
	case unix.S_IFREG, unix.S_IFDIR:
		if stat.Mode&unix.S_IFMT == unix.S_IFREG && (stat.Nlink != 1 || stat.Size > MaxImageBytes) {
			return Image{}, fileShapeRefusal(path, uint64(stat.Nlink))
		}
		fd, openErr := unix.Openat(
			int(parent.Fd()),
			name,
			unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
			0,
		)
		if openErr != nil {
			return Image{}, unreadFailure(path, stat.Mode&unix.S_IFMT == unix.S_IFDIR, openErr)
		}
		file := os.NewFile(uintptr(fd), path)
		defer func() { _ = file.Close() }()
		var opened unix.Stat_t
		if err = unix.Fstat(fd, &opened); err != nil {
			return Image{}, err
		}
		if opened.Ino != stat.Ino || opened.Dev != stat.Dev {
			return Image{}, Fail(
				ExitConflict,
				"conflict",
				display.Path(path)+" changed while Workbench read it",
				FixText("run the same command again"),
			)
		}
		directory := stat.Mode&unix.S_IFMT == unix.S_IFDIR
		if held, err = c.openMetadata(fd, path, directory); err != nil {
			return Image{}, err
		}
		image.Attributes, err = readImageAttributes(
			onFD(fd),
			path,
			holderOf(uint32(stat.Mode&unix.S_IFMT)),
		)
		if err != nil {
			return Image{}, err
		}
		if directory {
			image.Kind = ImageDirectory
		} else {
			image.Kind = ImageFile
			image.Data, err = io.ReadAll(io.LimitReader(file, MaxImageBytes+1))
			if err != nil {
				return Image{}, err
			}
		}
	default:
		return Image{}, specialFileRefusal(path, uint32(stat.Mode&unix.S_IFMT))
	}
	image.ACL, image.Hidden = held.acl, held.hidden
	if err = image.validate(c, path); err != nil {
		return Image{}, err
	}
	return image, nil
}

// openMetadata rejects metadata a checkpoint cannot preserve on the opened
// target at path and returns the ACL and hidden flag its image carries. A
// folder that holds Workbench's own files is only ever changed in place,
// never removed or replaced, and Workbench writes its own files in it, so it
// is held to containerMetadata's rules; any other folder to the file rules.
func (c Context) openMetadata(fd int, path string, directory bool) (security, error) {
	if directory && c.isContainer(path) {
		return containerMetadata(fd, path)
	}
	return fileMetadata(fd, path, folderOrFile(directory))
}

func sameImage(a, b Image) bool {
	if (a.Group == nil) != (b.Group == nil) || a.Group != nil && *a.Group != *b.Group {
		return false
	}
	return a.Kind == b.Kind && a.Mode == b.Mode && a.Link == b.Link &&
		bytes.Equal(a.Data, b.Data) && sameAttributes(a.Attributes, b.Attributes) &&
		bytes.Equal(a.ACL, b.ACL) && a.Hidden == b.Hidden
}

// sameImageContent is sameImage apart from the extended attributes, which
// the system may add to a file it writes, such as macOS's download
// provenance, and which the journal records as observed instead.
func sameImageContent(a, b Image) bool {
	a.Attributes = nil
	b.Attributes = nil
	return sameImage(a, b)
}
