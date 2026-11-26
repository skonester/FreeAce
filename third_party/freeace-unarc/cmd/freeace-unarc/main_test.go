package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n0madic/go-unarc/internal/arc"
)

const fixtureTime = 1700000000
const fixtureData = "firstsecond"

// A stored solid block contains two entries with the same basename in different
// directories. Keeping the records in one block exercises selection while decoding.
func fixture(table *crc32.Table, firstDirectory, data string) []byte {
	integer := func(out *bytes.Buffer, values ...int) {
		for _, value := range values {
			if value >= 128 {
				panic("fixture integers must fit in one FreeArc byte")
			}
			out.WriteByte(byte(value << 1))
		}
	}
	checksum := func(out *bytes.Buffer, data []byte) {
		out.Write(binary.LittleEndian.AppendUint32(nil, crc32.Checksum(data, table)))
	}
	header := []byte("ArC\x01\x00\x00\x06\x07")
	var directory bytes.Buffer
	integer(&directory, 1, 2)
	directory.WriteString("storing\x00")
	integer(&directory, len(data), len(data), 2)
	directory.WriteString(firstDirectory + "\x00other\x00example.txt\x00example.txt\x00")
	integer(&directory, 0, 1, len("first"), len("second"))
	for range 2 {
		directory.Write(binary.LittleEndian.AppendUint32(nil, fixtureTime))
	}
	directory.Write([]byte{0, 0})
	checksum(&directory, []byte("first"))
	checksum(&directory, []byte("second"))

	descriptor := func(out *bytes.Buffer, kind arc.BlockType, offset int, data []byte) {
		integer(out, int(kind))
		out.WriteString("storing\x00")
		if offset >= 0 {
			integer(out, offset)
		}
		integer(out, len(data), len(data))
		checksum(out, data)
	}
	var footer bytes.Buffer
	integer(&footer, 2)
	descriptor(&footer, arc.HeaderBlock, len(header)+len(data)+directory.Len(), header)
	descriptor(&footer, arc.DirBlock, directory.Len(), directory.Bytes())
	footer.Write([]byte{0, 0})

	var tail bytes.Buffer
	tail.WriteString("ArC\x01")
	descriptor(&tail, arc.FooterBlock, -1, footer.Bytes())
	checksum(&tail, tail.Bytes())
	return bytes.Join([][]byte{header, []byte(data), directory.Bytes(), footer.Bytes(), tail.Bytes()}, nil)
}

func writeFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFreeArcListingTestingAndExtraction(t *testing.T) {
	for name, table := range map[string]*crc32.Table{
		"standard.arc": crc32.IEEETable,
		"setup-1.bin":  crc32.MakeTable(0x8e092638),
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFixture(t, name, fixture(table, "folder", fixtureData))
			var listing bytes.Buffer
			if err := run([]string{"l", path}, &listing); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(listing.String(), "Type = FreeArc\n") ||
				!strings.Contains(listing.String(), "Path = folder/example.txt\nSize = 5\n") {
				t.Fatalf("unexpected listing: %s", &listing)
			}
			if err := run([]string{"t", path}, io.Discard); err != nil {
				t.Fatal(err)
			}
			out := t.TempDir()
			if err := run([]string{"x", path, out}, io.Discard); err != nil {
				t.Fatal(err)
			}
			item := filepath.Join(out, "folder", "example.txt")
			contents, err := os.ReadFile(item)
			if err != nil || string(contents) != "first" {
				t.Fatalf("extracted %q, error = %v", contents, err)
			}
			info, err := os.Stat(item)
			if err != nil {
				t.Fatal(err)
			}
			if !info.ModTime().Equal(time.Unix(fixtureTime, 0)) {
				t.Fatalf("extracted modification time = %v", info.ModTime())
			}
		})
	}
}

func TestSelectedMemberUsesFullPath(t *testing.T) {
	path := writeFixture(t, "setup-1.bin", fixture(crc32.IEEETable, "folder", fixtureData))
	list := writeFixture(t, "selection.txt", []byte("other\\example.txt\r\n"))
	out := t.TempDir()
	if err := run([]string{"x", path, out, list}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "other", "example.txt"))
	if err != nil || string(data) != "second" {
		t.Fatalf("extracted %q, error = %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(out, "folder")); !os.IsNotExist(err) {
		t.Fatalf("unselected directory exists, error = %v", err)
	}
}

func TestRejectsCorruptedDataAndUnsafePaths(t *testing.T) {
	for _, tc := range []struct{ directory, data, message string }{
		{"folder", "!irstsecond", "failed CRC check"},
		{"../outside", fixtureData, "Unsafe archive path"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			path := writeFixture(t, "setup-1.bin", fixture(crc32.MakeTable(0x8e092638), tc.directory, tc.data))
			out := t.TempDir()
			err := run([]string{"x", path, out}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("error = %v, expected %s", err, tc.message)
			}
			if err := filepath.WalkDir(out, func(path string, item os.DirEntry, err error) error {
				if err == nil && !item.IsDir() {
					t.Errorf("partial or corrupt output left at %s", path)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRejectsInvalidFooterChecksum(t *testing.T) {
	data := fixture(crc32.MakeTable(0x8e092638), "folder", fixtureData)
	data[len(data)-1] ^= 1
	path := writeFixture(t, "setup-1.bin", data)
	if err := run([]string{"l", path}, io.Discard); err == nil || !strings.Contains(err.Error(), "descriptor failed CRC") {
		t.Fatalf("error = %v, expected invalid footer checksum", err)
	}
}
