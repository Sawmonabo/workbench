package platform

import "errors"

// newFolderMode fails: macOS reads a umask only by setting it.
func newFolderMode(uint32) (uint32, error) { return 0, errors.ErrUnsupported }
