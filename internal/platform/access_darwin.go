package platform

// The kauth rights of sys/kauth.h for reading and searching:
// KAUTH_VNODE_READ_DATA and KAUTH_VNODE_EXECUTE; and the generic rights an
// entry may name instead, each of which holds a set of them:
// KAUTH_ACE_GENERIC_ALL holds every right, KAUTH_ACE_GENERIC_READ the
// KAUTH_VNODE_GENERIC_READ_BITS, KAUTH_ACE_GENERIC_WRITE the
// KAUTH_VNODE_GENERIC_WRITE_BITS and KAUTH_ACE_GENERIC_EXECUTE the
// KAUTH_VNODE_GENERIC_EXECUTE_BITS.
const (
	readRight      uint32 = 1 << 1
	searchRight    uint32 = 1 << 3
	genericAll     uint32 = 1 << 21
	genericExecute uint32 = 1 << 22
	genericWrite   uint32 = 1 << 23
	genericRead    uint32 = 1 << 24
	// The KAUTH_VNODE_GENERIC_*_BITS: READ_DATA, READ_ATTRIBUTES,
	// READ_EXTATTRIBUTES and READ_SECURITY; WRITE_DATA, APPEND_DATA, DELETE,
	// DELETE_CHILD, WRITE_ATTRIBUTES, WRITE_EXTATTRIBUTES and WRITE_SECURITY;
	// EXECUTE.
	genericReadBits    uint32 = readRight | 1<<7 | 1<<9 | 1<<11
	genericWriteBits   uint32 = 1<<2 | 1<<5 | 1<<4 | 1<<6 | 1<<8 | 1<<10 | 1<<12
	genericExecuteBits uint32 = searchRight
)

// genericHolding is the generic rights that hold one of rights:
// KAUTH_ACE_GENERIC_ALL always, and each other generic right whose bits
// share one with rights.
func genericHolding(rights uint32) uint32 {
	generic := genericAll
	if rights&genericReadBits != 0 {
		generic |= genericRead
	}
	if rights&genericWriteBits != 0 {
		generic |= genericWrite
	}
	if rights&genericExecuteBits != 0 {
		generic |= genericExecute
	}
	return generic
}
