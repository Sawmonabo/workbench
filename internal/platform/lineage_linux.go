package platform

// ProcessLineage always reports false: Linux keeps no record of the process
// that forked one once that parent exits and another adopts the child, so
// callers learn nothing from it here (KnowsOriginalParent is false).
func ProcessLineage(int) (Lineage, bool) {
	return Lineage{}, false
}
