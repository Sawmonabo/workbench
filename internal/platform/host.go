package platform

// HostRelease names the operating system release Workbench runs on, and ok is
// false when it cannot be learned. On macOS it is "macOS" and the product
// version /usr/bin/sw_vers -productVersion prints, such as "15.7.7", which
// run gives back; on Linux it is the ID and VERSION_ID of /etc/os-release,
// such as "ubuntu" and "24.04", and run is not called. run starts executable
// with args, in an environment that sets only PATH, and returns its standard
// output: this package starts no process, so its caller runs the command.
func HostRelease(
	run func(executable string, args ...string) (string, error),
) (name, version string, ok bool) {
	return hostRelease(run)
}
