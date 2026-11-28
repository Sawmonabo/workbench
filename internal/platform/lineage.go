package platform

// Lineage is the kernel's record of where a process came from, where the
// system keeps one (KnowsOriginalParent): Version names the process itself
// and ParentVersion the process that forked it, as that parent's Version was
// when it forked, so it no longer equals the parent's Version once the parent
// execs. It stays recorded after that parent exits and init adopts the child.
type Lineage struct{ Version, ParentVersion uint32 }
