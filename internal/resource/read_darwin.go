//go:build darwin

package resource

import "golang.org/x/sys/unix"

// Read samples available + total memory via mach VM sysctls. Every value is a
// single sysctl (no vm_stat fork, no /proc) so it is safe to call on the 1s
// cockpit tick — the acceptance criterion's "no measurable overhead": these
// are counters the kernel already maintains, read straight out.
//
// The page counters are 32-bit unsigned ints (integer_t); hw.memsize is the
// only 64-bit value. A missing counter reads as 0 rather than failing the
// whole sample — the available figure just degrades, it never lies upward.
func Read() (Mem, error) {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return Mem{}, err
	}
	pageSize, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return Mem{}, err
	}
	u32 := func(name string) uint64 {
		v, _ := unix.SysctlUint32(name)
		return uint64(v)
	}
	return computeMem(pageStats{
		pageSize:    uint64(pageSize),
		free:        u32("vm.page_free_count"),
		speculative: u32("vm.page_speculative_count"),
		purgeable:   u32("vm.page_purgeable_count"),
		external:    u32("vm.page_pageable_external_count"),
		total:       total,
	}), nil
}
