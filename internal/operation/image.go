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
// link, extended attributes and group.
type Image struct {
	Kind       ImageKind         `json:"kind"`
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
	shown := display.Path(path)
	switch {
	case image.Mode > 0o777:
		return Fail(
			ExitBlocked,
			"image",
			fmt.Sprintf(
				"An image of %s has mode %04o, beyond the 0777 an image keeps",
				shown,
				image.Mode,
			),
		)
	case len(image.Data) > MaxImageBytes:
		return Fail(
			ExitBlocked,
			"image",
			"An image of "+shown+" holds more than the "+display.Limit(MaxImageBytes)+
				" an image keeps of a file",
		)
	case len(image.Link) > 4096:
		return Fail(
			ExitBlocked,
			"image",
			"An image of "+shown+" links to a path longer than the 4096 bytes an image keeps",
		)
	}
	if image.Kind != ImageAbsent {
		if image.Group == nil {
			return Fail(ExitInvalid, "image", "An image of "+shown+" has no group")
		}
	}
	if image.Kind == ImageAbsent && len(image.Attributes) > 0 {
		return Fail(
			ExitBlocked,
			"metadata",
			"An image of "+shown+", which does not exist, holds extended attributes",
		)
	}
	for _, name := range slices.Sorted(maps.Keys(image.Attributes)) {
		held := "An image of " + shown + " holds the extended attribute " + shellWord(name)
		switch {
		case !slices.Contains(preservedAttributes, name):
			return Fail(ExitBlocked, "metadata", held+", which images do not carry")
		case len(image.Attributes[name]) > 4096:
			return Fail(ExitBlocked, "metadata", held+", larger than the 4 KiB an image keeps")
		}
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
		if len(image.Data) != 0 || image.Link == "" || image.Mode != 0o777 {
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
				shown+" is a link to itself, which Workbench does not keep; remove it or point it elsewhere, then retry",
			)
		}
		if err := c.validateImagePath(target); err != nil {
			return linkTargetError(path, target, err)
		}
		if err := safeParents(target); err != nil {
			return linkTargetError(path, target, err)
		}
	default:
		return Fail(ExitInvalid, "image", "Unknown image type for "+shown)
	}
	return nil
}

// linkTargetError is err, a refusal of target, which the link at path points
// to, led by the link, so a person knows which file the refusal is about.
func linkTargetError(path, target string, err error) error {
	if problem, ok := errors.AsType[*Error](err); ok {
		return Fail(
			problem.Code,
			problem.Category,
			display.Path(path)+" links to "+display.Path(target)+": "+problem.Message,
		)
	}
	return err
}

// Target parents are opened component by component without following links.
// All writes use that descriptor, so a concurrent parent rename cannot redirect
// them through an escaping symbolic link.
func imageParent(c Context, path string) (*os.File, string, error) {
	if err := c.validateImagePath(path); err != nil {
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
	if err = parentMetadata(fd, filepath.Dir(path)); err != nil {
		_ = unix.Close(fd)
		return nil, "", err
	}
	return os.NewFile(uintptr(fd), filepath.Dir(path)), filepath.Base(path), nil
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
		return Image{}, err
	}
	if int(stat.Uid) != os.Geteuid() || stat.Mode&0o7000 != 0 {
		return Image{}, ownershipRefusal(path, stat.Uid, uint32(stat.Mode))
	}
	group := uint32(stat.Gid)
	image := Image{Mode: uint32(stat.Mode) & 0o777, Group: &group}
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFLNK:
		data := make([]byte, 4097)
		n, readErr := unix.Readlinkat(int(parent.Fd()), name, data)
		if readErr != nil || n > 4096 {
			return Image{}, linkReadRefusal(path, readErr)
		}
		image.Kind, image.Link = ImageSymlink, string(data[:n])
		if err = linkMetadata(path); err != nil {
			return Image{}, err
		}
		image.Attributes, err = readImageAttributes(-1, path, replacedFile)
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
			return Image{}, openErr
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
				display.Path(path)+" changed while Workbench read it; retry",
			)
		}
		directory := stat.Mode&unix.S_IFMT == unix.S_IFDIR
		if err = c.openMetadata(fd, path, directory); err != nil {
			return Image{}, err
		}
		image.Attributes, err = readImageAttributes(fd, path, holderOf(directory))
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
	if err = image.validate(c, path); err != nil {
		return Image{}, err
	}
	return image, nil
}

// openMetadata rejects metadata a checkpoint cannot preserve on the opened
// target at path. A folder that holds Workbench's own files is only ever
// changed in place, never removed or replaced, so it may keep the ACL
// containerMetadata admits; any other directory is held to the file rules,
// because removing one and restoring it would drop that ACL.
func (c Context) openMetadata(fd int, path string, directory bool) error {
	if directory && c.isContainer(path) {
		return containerMetadata(fd, path)
	}
	return fileMetadata(fd, path, holderOf(directory))
}

func sameImage(a, b Image) bool {
	if (a.Group == nil) != (b.Group == nil) || a.Group != nil && *a.Group != *b.Group {
		return false
	}
	return a.Kind == b.Kind && a.Mode == b.Mode && a.Link == b.Link &&
		bytes.Equal(a.Data, b.Data) && sameAttributes(a.Attributes, b.Attributes)
}

func sameImageContent(a, b Image) bool {
	a.Attributes = nil
	b.Attributes = nil
	return sameImage(a, b)
}
