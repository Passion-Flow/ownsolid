package dameng

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"testing"
)

type nativeDynamicFixture struct {
	tag  elf.DynTag
	text string
}

// This constructs inert ELF metadata for the production parser. It is never
// loaded or executed and cannot qualify a vendor library or its runtime ABI.
func nativeELFFixture(t *testing.T, machine elf.Machine, entries ...nativeDynamicFixture) []byte {
	t.Helper()
	text := []byte{0}
	var dynamic bytes.Buffer
	for _, entry := range entries {
		value := uint64(len(text))
		text = append(text, []byte(entry.text)...)
		text = append(text, 0)
		if err := binary.Write(&dynamic, binary.LittleEndian, elf.Dyn64{Tag: int64(entry.tag), Val: value}); err != nil {
			t.Fatal(err)
		}
	}
	const dataOffset = 64 + 2*56
	const virtualBase = 0x1000
	var complete bytes.Buffer
	for _, entry := range []elf.Dyn64{{Tag: int64(elf.DT_STRTAB), Val: virtualBase + dataOffset}, {Tag: int64(elf.DT_STRSZ), Val: uint64(len(text))}} {
		if err := binary.Write(&complete, binary.LittleEndian, entry); err != nil {
			t.Fatal(err)
		}
	}
	complete.Write(dynamic.Bytes())
	if err := binary.Write(&complete, binary.LittleEndian, elf.Dyn64{}); err != nil {
		t.Fatal(err)
	}
	ident := [16]byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)}
	header := elf.Header64{Ident: ident, Type: uint16(elf.ET_DYN), Machine: uint16(machine), Version: uint32(elf.EV_CURRENT), Phoff: 64, Ehsize: 64, Phentsize: 56, Phnum: 2}
	total := uint64(dataOffset + len(text) + complete.Len())
	programs := []elf.Prog64{
		{Type: uint32(elf.PT_LOAD), Off: 0, Vaddr: virtualBase, Filesz: total, Memsz: total},
		{Type: uint32(elf.PT_DYNAMIC), Off: dataOffset + uint64(len(text)), Vaddr: virtualBase + dataOffset + uint64(len(text)), Filesz: uint64(complete.Len()), Memsz: uint64(complete.Len())},
	}
	var raw bytes.Buffer
	for _, value := range []any{header, programs} {
		if err := binary.Write(&raw, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	raw.Write(text)
	raw.Write(complete.Bytes())
	return raw.Bytes()
}

func TestNativeLibraryDependencyAndPrivateLoaderBoundary(t *testing.T) {
	files := map[string]bool{"libdodbc.so": true, "libdmclient.so": true}
	for _, target := range []struct {
		architecture string
		machine      elf.Machine
	}{{"amd64", elf.EM_X86_64}, {"arm64", elf.EM_AARCH64}} {
		raw := nativeELFFixture(t, target.machine,
			nativeDynamicFixture{elf.DT_SONAME, "libdodbc.so"},
			nativeDynamicFixture{elf.DT_NEEDED, "libc.so.6"},
			nativeDynamicFixture{elf.DT_NEEDED, "libdmclient.so"},
			nativeDynamicFixture{elf.DT_RUNPATH, "$ORIGIN"})
		if err := inspectNativeLibrary(bytes.NewReader(raw), "libdodbc.so", target.architecture, files); err != nil {
			t.Fatal("declared native metadata refused", target.architecture, err)
		}
	}
	for name, entry := range map[string]nativeDynamicFixture{
		"unlisted_dependency": {elf.DT_NEEDED, "libunlisted.so"},
		"wrong_loader_arch":   {elf.DT_NEEDED, "ld-linux-aarch64.so.1"},
		"host_cpp_fallback":   {elf.DT_NEEDED, "libstdc++.so.6"},
		"absolute_dependency": {elf.DT_NEEDED, "/tmp/libdmclient.so"},
		"relative_dependency": {elf.DT_NEEDED, "sub/libdmclient.so"},
		"soname_alias":        {elf.DT_SONAME, "other.so"},
		"absolute_rpath":      {elf.DT_RPATH, "/usr/local/lib"},
		"relative_runpath":    {elf.DT_RUNPATH, "lib"},
		"origin_escape":       {elf.DT_RUNPATH, "$ORIGIN/.."},
		"current_directory":   {elf.DT_RUNPATH, "$ORIGIN:"},
		"filter_loader":       {elf.DT_FILTER, "libdmclient.so"},
		"auxiliary_loader":    {elf.DT_AUXILIARY, "libdmclient.so"},
		"audit_loader":        {0x6ffffefc, "libdmclient.so"},
		"dependency_audit":    {0x6ffffefb, "libdmclient.so"},
		"loader_config":       {0x6ffffefa, "config"},
	} {
		t.Run(name, func(t *testing.T) {
			raw := nativeELFFixture(t, elf.EM_X86_64, entry)
			if inspectNativeLibrary(bytes.NewReader(raw), "libdodbc.so", "amd64", files) == nil {
				t.Fatal("undeclared native loader authority admitted")
			}
		})
	}
	// libstdc++ must be an exact pinned inventory entry, rather than a global
	// loader fallback. Its conventional '+' filename remains a safe basename.
	cpp := nativeELFFixture(t, elf.EM_X86_64, nativeDynamicFixture{elf.DT_NEEDED, "libstdc++.so.6"})
	if err := inspectNativeLibrary(bytes.NewReader(cpp), "libdodbc.so", "amd64", map[string]bool{"libdodbc.so": true, "libstdc++.so.6": true}); err != nil {
		t.Fatal("explicit C++ support inventory refused", err)
	}
	for _, tag := range []elf.DynTag{elf.DT_NEEDED, elf.DT_SONAME, elf.DT_RPATH, elf.DT_RUNPATH} {
		text := "libdodbc.so"
		if tag == elf.DT_RPATH || tag == elf.DT_RUNPATH {
			text = "${ORIGIN}"
		}
		raw := nativeELFFixture(t, elf.EM_X86_64, nativeDynamicFixture{tag, text}, nativeDynamicFixture{tag, text})
		if inspectNativeLibrary(bytes.NewReader(raw), "libdodbc.so", "amd64", files) == nil {
			t.Fatal("ambiguous duplicate dynamic tag admitted", tag)
		}
	}
}

func TestNativeLibraryRejectsMalformedOrUnboundedMetadata(t *testing.T) {
	files := map[string]bool{"libdodbc.so": true}
	for name, mutate := range map[string]func([]byte){
		"wrong_architecture":       func(raw []byte) { binary.LittleEndian.PutUint16(raw[18:], uint16(elf.EM_AARCH64)) },
		"executable":               func(raw []byte) { binary.LittleEndian.PutUint16(raw[16:], uint16(elf.ET_EXEC)) },
		"not_elf":                  func(raw []byte) { raw[0] = 0 },
		"missing_dynamic":          func(raw []byte) { binary.LittleEndian.PutUint32(raw[120:], uint32(elf.PT_NULL)) },
		"dynamic_mapping_mismatch": func(raw []byte) { binary.LittleEndian.PutUint64(raw[136:], 0x1000) },
		"invalid_load_size":        func(raw []byte) { binary.LittleEndian.PutUint64(raw[104:], 1) },
		"oversize_strings": func(raw []byte) {
			offset := binary.LittleEndian.Uint64(raw[128:])
			binary.LittleEndian.PutUint64(raw[offset+24:], maximumNativeStringBytes+1)
		},
		"oversize_dynamic":  func(raw []byte) { binary.LittleEndian.PutUint64(raw[152:], maximumNativeDynamicBytes+16) },
		"interpreter":       func(raw []byte) { binary.LittleEndian.PutUint32(raw[120:], uint32(elf.PT_INTERP)) },
		"ambiguous_dynamic": func(raw []byte) { binary.LittleEndian.PutUint32(raw[64:], uint32(elf.PT_DYNAMIC)) },
		"bad_string_offset": func(raw []byte) {
			offset := binary.LittleEndian.Uint64(raw[128:])
			binary.LittleEndian.PutUint64(raw[offset+32+8:], 1<<32)
		},
		"missing_terminator": func(raw []byte) { binary.LittleEndian.PutUint64(raw[len(raw)-16:], uint64(elf.DT_DEBUG)) },
		"nonzero_null":       func(raw []byte) { binary.LittleEndian.PutUint64(raw[len(raw)-8:], 1) },
		"premature_null": func(raw []byte) {
			offset := binary.LittleEndian.Uint64(raw[128:])
			clear(raw[offset+32 : offset+48])
		},
	} {
		t.Run(name, func(t *testing.T) {
			raw := nativeELFFixture(t, elf.EM_X86_64, nativeDynamicFixture{elf.DT_SONAME, "libdodbc.so"}, nativeDynamicFixture{elf.DT_NEEDED, "libc.so.6"})
			mutate(raw)
			if inspectNativeLibrary(bytes.NewReader(raw), "libdodbc.so", "amd64", files) == nil {
				t.Fatal("malformed native metadata admitted")
			}
		})
	}
}
