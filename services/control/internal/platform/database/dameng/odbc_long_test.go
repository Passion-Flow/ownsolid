//go:build cgo && odbc_manager

package dameng

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"
)

func longNativeConfig() ODBCConfig {
	c := odbcNativeTestConfig()
	c.FormatVersion = 3
	c.MaximumParameterBytes = 65536
	c.MaximumLongValueBytes = 2 << 20
	c.TransferChunkBytes = 4096
	c.MaximumRetainedBytes = 16 << 20
	return c
}
func TestLongBindingsUseDAEAndPrivateNativeChunks(t *testing.T) {
	c := longNativeConfig()
	m, e := NewODBCManagerForValidation(c)
	if e != nil {
		t.Fatal(e)
	}
	defer odbcCloseNativeTestManager(t, m)
	lease, _ := m.memoryLease()
	text := strings.Repeat("a😀", 2000)
	binary := bytes.Repeat([]byte{0, 1, 255}, 5000)
	p, e := odbcBuildParametersOwned([]driver.NamedValue{{Ordinal: 1, Value: text}, {Ordinal: 2, Value: binary}}, c, lease)
	if e != nil {
		t.Fatal(e)
	}
	defer p.free()
	if p.transfer == nil || p.streams[0] != text || len(p.streams) != 2 || m.Status().RetainedBytes == 0 {
		t.Fatal("DAE ownership missing")
	}
	if unsafe.StringData(p.streams[0].(string)) == unsafe.StringData(text) {
		t.Fatal("stream retained a potentially larger caller string backing")
	}
	binary[0] = 88
	if p.streams[1].([]byte)[0] != 0 {
		t.Fatal("stream retained mutable caller bytes")
	}
	var units []uint16
	offset := 0
	for offset < len(text) {
		n, next, e := odbcTransferChunk(text, offset, p.transfer, p.chunkBytes)
		if e != nil || next <= offset || n > p.chunkBytes || n%2 != 0 {
			t.Fatal("invalid text chunk", e)
		}
		units = append(units, unsafe.Slice((*uint16)(p.transfer), n/2)...)
		offset = next
	}
	if string(utf16.Decode(units)) != text {
		t.Fatal("UTF16 transfer changed text")
	}
	p.free()
	if s := m.Status(); s.RetainedBytes != 0 || s.MemoryLeases != 0 {
		t.Fatal("binding memory retained after disposal")
	}
}
func TestLongFrameRejectsTruncationAndUnknownFinalLength(t *testing.T) {
	f := odbcLongFrame{}
	if n, _, e := f.accept(10, true, 4); e != nil || n != 4 {
		t.Fatal(e)
	}
	if _, _, e := f.accept(0, false, 4); e == nil {
		t.Fatal("early terminal success silently truncated")
	}
	f = odbcLongFrame{}
	for _, part := range []struct {
		length int64
		more   bool
		want   int
	}{{odbcNoTotal, true, 4}, {6, true, 4}, {2, false, 2}} {
		if n, null, e := f.accept(part.length, part.more, 4); e != nil || null || n != part.want {
			t.Fatal("valid continuation refused", e)
		}
	}
	for _, part := range []struct {
		length int64
		more   bool
	}{{odbcNoTotal, false}, {-999, true}, {3, true}, {4, true}, {5, false}} {
		f = odbcLongFrame{}
		if _, _, e := f.accept(part.length, part.more, 4); e == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	f = odbcLongFrame{consumed: 1, seen: true}
	if _, _, e := f.accept(1<<63-1, true, 4); e == nil {
		t.Fatal("remaining length addition overflow accepted")
	}
	f = odbcLongFrame{}
	if _, null, e := f.accept(odbcNullData, false, 4); e != nil || !null {
		t.Fatal("NULL changed")
	}
}
func TestLongFrameTerminalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name         string
		initial      []int64
		final        int64
		want         int
		null, reject bool
	}{
		{"empty-first", nil, 0, 0, false, false},
		{"null-first", nil, odbcNullData, 0, true, false},
		{"exact-payload-first", nil, 4, 4, false, false},
		{"unknown-positive-final", []int64{odbcNoTotal}, 2, 2, false, false},
		{"unknown-exact-final", []int64{odbcNoTotal}, 4, 4, false, false},
		{"unknown-zero-final", []int64{odbcNoTotal}, 0, 0, false, true},
		{"multiple-unknown-zero-final", []int64{odbcNoTotal, odbcNoTotal}, 0, 0, false, true},
		{"unknown-null-final", []int64{odbcNoTotal}, odbcNullData, 0, false, true},
		{"unknown-unknown-final", []int64{odbcNoTotal}, odbcNoTotal, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := odbcLongFrame{}
			for _, indicator := range tc.initial {
				if n, null, e := frame.accept(indicator, true, 4); e != nil || null || n != 4 {
					t.Fatal("valid continuation rejected", e)
				}
			}
			before := frame
			n, null, e := frame.accept(tc.final, false, 4)
			if tc.reject {
				if e == nil || e.Error() != "DAMENG_ODBC_LONG_VALUE_LENGTH_INVALID" || frame != before {
					t.Fatal("invalid terminal frame accepted or mutated accumulator state", e)
				}
				return
			}
			if e != nil || n != tc.want || null != tc.null {
				t.Fatal("valid terminal frame changed", e)
			}
			if !null && (!frame.known || frame.consumed != int64(4*len(tc.initial)+tc.want) || frame.total != frame.consumed) {
				t.Fatal("terminal byte total mismatch")
			}
		})
	}
}

func TestLongLegacyRetainedBudgetPreservesValidColumnLimits(t *testing.T) {
	for _, columns := range []int{6, 64, 128} {
		for _, physical := range []int{1, 4} {
			t.Run(fmt.Sprintf("columns-%d/physical-%d", columns, physical), func(t *testing.T) {
				c := odbcNativeTestConfig()
				c.MaximumParameterBytes = 4096
				c.MaximumCellBytes = 256
				c.MaximumResultBytes = 2048
				c.MaximumColumns = columns
				c.MaximumOpenConnections = 1
				c.MaximumPhysicalConnections = physical
				if c.FormatVersion != 2 || c.Validate() != nil || c.Capacity().LongValues {
					t.Fatal("legacy valid policy changed")
				}
				m, e := NewODBCManagerForValidation(c)
				if e != nil {
					t.Fatal(e)
				}
				defer odbcCloseNativeTestManager(t, m)
				// Use production leases and C scratch, without claiming a live query.
				// Each physical slot admits the metadata reservation of even a tiny
				// query plus its scratch, row and temporary scalar conversion bound.
				for i := 0; i < physical; i++ {
					scratch, e := m.memoryLease()
					if e != nil {
						t.Fatal(e)
					}
					defer scratch.close()
					buffer, e := odbcNewReadBuffer(max(c.MaximumCellBytes+2, 32), scratch)
					if e != nil {
						t.Fatal(e)
					}
					defer buffer.free()
					result, e := m.memoryLease()
					if e != nil {
						t.Fatal(e)
					}
					defer result.close()
					for _, bytes := range []int{c.MaximumColumns * odbcColumnMemoryAllowance, 32 + 24, c.MaximumCellBytes*4 + 64} {
						if e := result.grow(bytes); e != nil {
							t.Fatal("legacy tiny-query reservation refused", e)
						}
					}
				}
				boundary, e := m.memoryLease()
				if e != nil {
					t.Fatal(e)
				}
				defer boundary.close()
				if e := boundary.grow(c.retainedLimit() - m.Status().RetainedBytes); e != nil {
					t.Fatal(e)
				}
				if s := m.Status(); s.RetainedBytes != s.RetainedLimit {
					t.Fatal("exact retained limit refused")
				}
				if e := boundary.grow(1); e == nil || m.Status().RetainedBytes != c.retainedLimit() {
					t.Fatal("legacy derived budget overflow accepted")
				}
			})
		}
	}
}

func TestLongUnicodeAccumulatorChecksSplitAndExactBudget(t *testing.T) {
	a := odbcLongAccumulator{maximum: 5}
	if e := a.wide([]uint16{'a', 0xd83d}, false); e != nil {
		t.Fatal(e)
	}
	if e := a.wide([]uint16{0xde00}, true); e != nil || string(a.value) != "a😀" {
		t.Fatal("split surrogate changed", e)
	}
	if e := a.wide([]uint16{'b'}, true); e == nil || string(a.value) != "a😀" {
		t.Fatal("oversize appended before refusal")
	}
	for _, units := range [][]uint16{{0}, {0xdc00}, {0xd800}, {0xd800, 'x'}} {
		a = odbcLongAccumulator{maximum: 20}
		if e := a.wide(units, true); e == nil {
			t.Fatal("invalid UTF16 accepted")
		}
	}
}
func TestLongMemoryReservedBeforeAllocationAndRowsRelease(t *testing.T) {
	c := longNativeConfig()
	m, _ := NewODBCManagerForValidation(c)
	defer odbcCloseNativeTestManager(t, m)
	full, _ := m.memoryLease()
	if e := full.grow(c.MaximumRetainedBytes); e != nil {
		t.Fatal(e)
	}
	blocked, _ := m.memoryLease()
	p, e := odbcBuildParametersOwned([]driver.NamedValue{{Ordinal: 1, Value: strings.Repeat("a", 5000)}}, c, blocked)
	if e == nil || p.pointer != nil || p.transfer != nil {
		p.free()
		t.Fatal("native allocation before budget admission")
	}
	blocked.close()
	full.close()
	lease, _ := m.memoryLease()
	if e := lease.grow(1024); e != nil {
		t.Fatal(e)
	}
	r := &odbcRows{ctx: context.Background(), columns: []string{"x"}, rows: [][]driver.Value{{"value"}}, memory: lease}
	if e := r.Next(make([]driver.Value, 1)); e != nil || m.Status().RetainedBytes != 1024 {
		t.Fatal("consumption prematurely released result lease")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if status, e := m.Close(ctx); e == nil || status.State == "closed" {
		t.Fatal("manager claimed closed with live result")
	}
	r.Close()
	r.Close()
	if m.Status().RetainedBytes != 0 || m.Status().MemoryLeases != 0 {
		t.Fatal("result lease not released exactly once")
	}
}
func TestLongBuffersSurviveOriginalAndCancelJoin(t *testing.T) {
	c := longNativeConfig()
	m, _ := NewODBCManagerForValidation(c)
	defer odbcCloseNativeTestManager(t, m)
	memory, _ := m.memoryLease()
	buffer, e := odbcNewReadBuffer(4096, memory)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started, original, ack, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := joinODBC(ctx, func() error { close(ack); <-release; return nil }, nil, func() (any, error) { close(started); <-original; return nil, nil })
		buffer.free()
		done <- e
	}()
	<-started
	cancel()
	<-ack
	close(original)
	if m.Status().RetainedBytes != 4096 {
		t.Fatal("buffer released before cancellation joined")
	}
	select {
	case <-done:
		t.Fatal("early join")
	default:
	}
	close(release)
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if m.Status().RetainedBytes != 0 {
		t.Fatal("joined buffer retained")
	}
}

func TestLongDAETokensRefuseForeignAndDuplicateDescriptors(t *testing.T) {
	c := longNativeConfig()
	m, e := NewODBCManagerForValidation(c)
	if e != nil {
		t.Fatal(e)
	}
	defer odbcCloseNativeTestManager(t, m)
	lease, _ := m.memoryLease()
	p, e := odbcBuildParametersOwned([]driver.NamedValue{{Ordinal: 1, Value: strings.Repeat("x", 5000)}}, c, lease)
	if e != nil {
		t.Fatal(e)
	}
	defer p.free()
	seen := make([]bool, 1)
	if _, e := p.takeStream(p.transfer, seen); e == nil || seen[0] {
		t.Fatal("foreign token admitted")
	}
	if _, e := p.takeStream(nil, seen); e == nil {
		t.Fatal("nil token admitted")
	}
	if _, e := p.takeStream(unsafe.Pointer(p.pointer), seen); e != nil || !seen[0] {
		t.Fatal("native descriptor token refused", e)
	}
	if _, e := p.takeStream(unsafe.Pointer(p.pointer), seen); e == nil {
		t.Fatal("duplicate token admitted")
	}
}

func TestLongStatementFreeFailureRetainsScratchAndBindings(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{fmt.Sprint("free:", odbcStatementHandle): 1}}
	// This cleanup port is synthetic; real C buffers and production release logic
	// are exercised, but no fake handle enters ODBC and no DM success is asserted.
	m := odbcOwnerTestManager(t, faults, 1)
	c := odbcOwnedSyntheticConnection(t, m)
	c.mu.Lock()
	entry, e := c.reserveStatement()
	if e != nil {
		c.mu.Unlock()
		t.Fatal(e)
	}
	entry.handle = odbcSyntheticHandle()
	memory, _ := m.memoryLease()
	entry.readBuffer, e = odbcNewReadBuffer(4096, memory)
	if e != nil {
		c.mu.Unlock()
		t.Fatal(e)
	}
	bindingMemory, _ := m.memoryLease()
	parameters, e := odbcBuildParametersOwned([]driver.NamedValue{{Ordinal: 1, Value: "synthetic"}}, m.cfg, bindingMemory)
	if e != nil {
		c.mu.Unlock()
		t.Fatal(e)
	}
	entry.parameters = &parameters
	before := m.Status().RetainedBytes
	e = c.releaseStatement(entry)
	retained := entry.readBuffer != nil && entry.readBuffer.pointer != nil && entry.parameters != nil && entry.parameters.pointer != nil
	c.mu.Unlock()
	if e == nil || !retained || m.Status().RetainedBytes != before {
		t.Fatal("failed native free lost scratch ownership")
	}
	ctx, cancel := odbcBoundedOwnerContext()
	defer cancel()
	if s, e := m.RetryCleanup(ctx); e != nil || s.RetainedBytes != 0 || s.MemoryLeases != 0 || s.PhysicalOwners != 0 {
		t.Fatal("explicit cleanup did not release joined scratch", e)
	}
}

func TestLongKnowledgeCapacityUsesStreamsAndBoundedFiveMiBResults(t *testing.T) {
	c := longNativeConfig()
	c.MaximumParameterBytes = 1 << 20
	c.MaximumLongValueBytes = 2 << 20
	c.MaximumResultBytes = 5 << 20
	c.MaximumRetainedBytes = 32 << 20
	m, e := NewODBCManagerForValidation(c)
	if e != nil {
		t.Fatal(e)
	}
	defer odbcCloseNativeTestManager(t, m)
	// One actual binding shape exceeds3MiB while the original inline cap remains1MiB.
	text := strings.Repeat("A\u4e2d😀", 262144)
	binary := bytes.Repeat([]byte{7}, (1<<20)+1)
	binding, _ := m.memoryLease()
	p, e := odbcBuildParametersOwned([]driver.NamedValue{{Ordinal: 1, Value: text}, {Ordinal: 2, Value: binary}}, c, binding)
	if e != nil {
		t.Fatal("M12-sized inputs did not use DAE", e)
	}
	defer p.free()
	if len(text) != 2<<20 || len(text)+len(binary) <= 3<<20 || p.streams[0] == nil || p.streams[1] == nil || p.transfer == nil {
		t.Fatal("capacity fixture did not exercise required shape")
	}
	retained, _ := m.memoryLease()
	defer retained.close()
	values := make([]driver.Value, 0, 3)
	for i := 0; i < 2; i++ {
		a := odbcLongAccumulator{maximum: c.MaximumLongValueBytes, memory: retained}
		offset := 0
		for offset < len(text) {
			n, next, e := odbcTransferChunk(text, offset, p.transfer, p.chunkBytes)
			if e != nil {
				t.Fatal(e)
			}
			offset = next
			if e = a.wide(unsafe.Slice((*uint16)(p.transfer), n/2), offset == len(text)); e != nil {
				t.Fatal(e)
			}
		}
		if string(a.value) != text {
			t.Fatal("2MiB Unicode value changed")
		}
		values = append(values, string(a.value))
	}
	a := odbcLongAccumulator{maximum: c.MaximumLongValueBytes, memory: retained}
	if e = a.appendBytes(binary[:1<<20]); e != nil {
		t.Fatal(e)
	}
	values = append(values, a.value)
	rows := &odbcRows{ctx: context.Background(), columns: []string{"a", "b", "c"}, rows: [][]driver.Value{values}, memory: retained}
	defer rows.Close()
	if retained.bytes != 4*(5<<20) || m.Status().RetainedBytes > c.MaximumRetainedBytes {
		t.Fatal("5MiB decoded result not charged to shared retained budget")
	}
	// Overlong data is rejected before copying or native allocation, not silently truncated.
	overlong, _ := m.memoryLease()
	invalid, e := odbcBuildParametersOwned([]driver.NamedValue{{Ordinal: 1, Value: text + "x"}}, c, overlong)
	if e == nil || invalid.pointer != nil {
		invalid.free()
		t.Fatal("overlong value admitted")
	}
	p.free()
	if e := rows.Next(make([]driver.Value, 3)); e != nil {
		t.Fatal(e)
	}
	if m.Status().RetainedBytes != 4*(5<<20) {
		t.Fatal("consuming large row released its lease before Close")
	}
	rows.Close()
	if s := m.Status(); s.RetainedBytes != 0 || s.MemoryLeases != 0 {
		t.Fatal("large shape leaked memory lease")
	}
}
