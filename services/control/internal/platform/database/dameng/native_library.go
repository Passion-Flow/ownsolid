package dameng

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"io"
	"math"
)

// Bound metadata before materializing the native loader tables. Vendor allocations
// and runtime dlopen behavior remain separate native qualification requirements.
const maximumNativeDynamicBytes = 65536
const maximumNativeStringBytes = 1 << 20

// These are the base glibc ABI dependencies of the declared Linux profile.
// Other libraries, including libstdc++, libgcc_s and libnsl, must be inventoried
// private files; an arbitrary host-installed dependency is never a substitute.
func nativeBaseLibrary(name string) bool {
	switch name {
	case "libc.so.6", "libm.so.6", "libpthread.so.0", "libdl.so.2", "librt.so.1", "ld-linux-x86-64.so.2", "ld-linux-aarch64.so.1":
		return true
	}
	return false
}

func nativeBaseDependency(name, architecture string) bool {
	if name == "ld-linux-x86-64.so.2" {
		return architecture == "amd64"
	}
	if name == "ld-linux-aarch64.so.1" {
		return architecture == "arm64"
	}
	return nativeBaseLibrary(name)
}

func inspectNativeLibrary(input io.ReaderAt, name, architecture string, files map[string]bool) error {
	return inspectNativeLibraryProfile(input, name, architecture, files, "")
}

func inspectNativeLibraryProfile(input io.ReaderAt, name, architecture string, files map[string]bool, profile string) error {
	bad := fail("DAMENG_NATIVE_ARTIFACT_INVALID")
	if profile != "" && profile != RelativeReleaseLoaderProfile {
		return bad
	}
	f, err := elf.NewFile(input)
	if err != nil {
		return bad
	}
	expected := elf.EM_X86_64
	if architecture == "arm64" {
		expected = elf.EM_AARCH64
	} else if architecture != "amd64" {
		return bad
	}
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Type != elf.ET_DYN || f.Machine != expected || f.Version != elf.EV_CURRENT {
		return bad
	}
	var dynamic *elf.Prog
	for _, program := range f.Progs {
		if program.Type == elf.PT_INTERP {
			return bad
		}
		if program.Type == elf.PT_DYNAMIC {
			if dynamic != nil {
				return bad
			}
			dynamic = program
		}
	}
	if dynamic == nil || dynamic.Filesz == 0 || dynamic.Filesz > maximumNativeDynamicBytes || dynamic.Filesz%16 != 0 || dynamic.Memsz < dynamic.Filesz || dynamic.Off > math.MaxInt64 {
		return bad
	}
	actualOffset, mapped := nativeMappedOffset(f.Progs, dynamic.Vaddr, dynamic.Filesz)
	if !mapped || actualOffset != dynamic.Off {
		return bad
	}
	table := make([]byte, int(dynamic.Filesz))
	if _, err := input.ReadAt(table, int64(dynamic.Off)); err != nil {
		return bad
	}
	// Read the same PT_DYNAMIC/DT_STRTAB mapping used by the native loader.
	// Section headers are not authoritative and may disagree with that mapping.
	var address, size uint64
	addressSeen, sizeSeen := false, false
	for at := 0; at < len(table); at += 16 {
		tag := elf.DynTag(binary.LittleEndian.Uint64(table[at:]))
		value := binary.LittleEndian.Uint64(table[at+8:])
		if tag == elf.DT_NULL {
			break
		}
		if tag == elf.DT_STRTAB {
			if addressSeen {
				return bad
			}
			address, addressSeen = value, true
		}
		if tag == elf.DT_STRSZ {
			if sizeSeen {
				return bad
			}
			size, sizeSeen = value, true
		}
	}
	if !addressSeen || !sizeSeen || size == 0 || size > maximumNativeStringBytes {
		return bad
	}
	offset, mapped := nativeMappedOffset(f.Progs, address, size)
	if !mapped {
		return bad
	}
	text := make([]byte, int(size))
	if _, err := input.ReadAt(text, int64(offset)); err != nil || text[0] != 0 {
		return bad
	}
	terminated := false
	seen := map[string]bool{}
	soname, rpath, runpath := false, false, false
	for at := 0; at < len(table); at += 16 {
		tag := elf.DynTag(binary.LittleEndian.Uint64(table[at:]))
		value := binary.LittleEndian.Uint64(table[at+8:])
		if tag == elf.DT_NULL {
			if value != 0 {
				return bad
			}
			terminated = true
			continue
		}
		if terminated {
			return bad
		}
		switch tag {
		case elf.DT_FILTER, elf.DT_AUXILIARY, 0x6ffffefa, 0x6ffffefb, 0x6ffffefc:
			// Dynamic filters, configuration and audit objects introduce a
			// second loader authority outside the declared DT_NEEDED closure.
			return bad
		case elf.DT_NEEDED, elf.DT_SONAME, elf.DT_RPATH, elf.DT_RUNPATH:
		default:
			continue
		}
		if value >= uint64(len(text)) {
			return bad
		}
		end := bytes.IndexByte(text[value:], 0)
		if end <= 0 {
			return bad
		}
		entry := string(text[value : value+uint64(end)])
		switch tag {
		case elf.DT_NEEDED:
			if !nativeFileName.MatchString(entry) || seen[entry] || !files[entry] && !nativeBaseDependency(entry, architecture) {
				return bad
			}
			seen[entry] = true
		case elf.DT_SONAME:
			// The caller has validated the manifest's byte-identical regular
			// installer pair before reaching this ELF-only metadata check.
			installerAlias := profile == RelativeReleaseLoaderProfile && name == nativeInstallerAlias && entry == nativeInstallerSONAME && files[nativeInstallerAlias] && files[nativeInstallerSONAME]
			if soname || entry != name && !installerAlias {
				return bad
			}
			soname = true
		case elf.DT_RPATH, elf.DT_RUNPATH:
			allowed := entry == "$ORIGIN" || entry == "${ORIGIN}" || profile == RelativeReleaseLoaderProfile && (entry == "./:../release" || entry == "./:../release:$ORIGIN:$ORIGIN/dependencies")
			if !allowed || tag == elf.DT_RPATH && rpath || tag == elf.DT_RUNPATH && runpath {
				return bad
			}
			rpath = rpath || tag == elf.DT_RPATH
			runpath = runpath || tag == elf.DT_RUNPATH
		}
	}
	if !terminated {
		return bad
	}
	return nil
}

// Translate only an unambiguous, file-backed PT_LOAD range. Both dynamic and
// string tables must match the mapping actually consumed by the Linux loader.
func nativeMappedOffset(programs []*elf.Prog, address, size uint64) (uint64, bool) {
	if size == 0 || size > math.MaxUint64-address {
		return 0, false
	}
	mapped := false
	var offset uint64
	for _, program := range programs {
		if program.Type != elf.PT_LOAD {
			continue
		}
		if program.Filesz > program.Memsz || program.Memsz > math.MaxUint64-program.Vaddr {
			return 0, false
		}
		if address < program.Vaddr || address-program.Vaddr > program.Filesz || size > program.Filesz-(address-program.Vaddr) {
			continue
		}
		delta := address - program.Vaddr
		if mapped || program.Off > math.MaxInt64 || delta > math.MaxInt64-program.Off {
			return 0, false
		}
		offset, mapped = program.Off+delta, true
	}
	return offset, mapped && offset <= math.MaxInt64-size
}
