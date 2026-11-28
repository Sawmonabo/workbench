package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	if image.Mode > 0o777 || len(image.Data) > MaxImageBytes || len(image.Link) > 4096 {
		return Fail(
			ExitBlocked,
			"image",
			"Target image exceeds the supported byte or permission bounds",
		)
	}
	if image.Kind != ImageAbsent {
		if image.Group == nil {
			return Fail(ExitInvalid, "image", "Present target image requires an explicit group")
		}
	}
	for name, value := range image.Attributes {
		if !slices.Contains(preservedAttributes, name) || len(value) > 4096 ||
			image.Kind == ImageAbsent {
			return Fail(ExitBlocked, "metadata", "Unsupported target extended attributes")
		}
	}
	switch image.Kind {
	case ImageAbsent:
		if image.Mode != 0 || len(image.Data) != 0 || image.Link != "" || image.Group != nil {
			return Fail(ExitInvalid, "image", "Malformed absent image")
		}
	case ImageFile:
		if image.Link != "" {
			return Fail(ExitInvalid, "image", "Malformed regular file image")
		}
	case ImageDirectory:
		if len(image.Data) != 0 || image.Link != "" {
			return Fail(ExitInvalid, "image", "Malformed directory image")
		}
	case ImageSymlink:
		if len(image.Data) != 0 || image.Link == "" || image.Mode != 0o777 {
			return Fail(ExitInvalid, "image", "Malformed symbolic link image")
		}
		target := image.Link
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		target = filepath.Clean(target)
		if target == path {
			return Fail(ExitBlocked, "image", "Self-referential target links are unsupported")
		}
		if err := c.validateImagePath(target); err != nil {
			return err
		}
		if err := safeParents(target); err != nil {
			return err
		}
	default:
		return Fail(ExitInvalid, "image", "Unknown target image type")
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
		return Image{}, Fail(
			ExitBlocked,
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
			return Image{}, Fail(ExitBlocked, "image", "Cannot capture target link")
		}
		image.Kind, image.Link = ImageSymlink, string(data[:n])
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
				ExitBlocked,
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
			return Image{}, Fail(ExitConflict, "conflict", "Target changed during image capture")
		}
		if err = c.openMetadata(fd, path, stat.Mode&unix.S_IFMT == unix.S_IFDIR); err != nil {
			return Image{}, err
		}
		image.Attributes, err = readImageAttributes(fd, path)
		if err != nil {
			return Image{}, err
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			image.Kind = ImageDirectory
		} else {
			image.Kind = ImageFile
			image.Data, err = io.ReadAll(io.LimitReader(file, MaxImageBytes+1))
			if err != nil {
				return Image{}, err
			}
		}
	default:
		return Image{}, Fail(ExitBlocked, "image", "Special filesystem targets are not supported")
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
	return fileMetadata(fd, path)
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
