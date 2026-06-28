package socketd

import (
	"encoding/binary"
	"testing"
)

func TestProtocolRecordSizes(t *testing.T) {
	cases := map[string]int{
		"container": containerRecordSize,
		"inspect":   inspectRecordSize,
		"event":     eventRecordSize,
	}
	want := map[string]int{
		"container": 2817,
		"inspect":   111553,
		"event":     369,
	}
	for name, got := range cases {
		if got != want[name] {
			t.Fatalf("%s record size = %d, want %d", name, got, want[name])
		}
	}
}

func TestParseContainerRecord(t *testing.T) {
	buf := make([]byte, containerRecordSize)
	off := 0
	copy(buf[off:off+recordNameMax], "alpine")
	off += recordNameMax
	copy(buf[off:off+uuidLen+1], "0123456789abcdef0123456789abcdef")
	off += uuidLen + 1
	copy(buf[off:off+recordPathMax], "/var/lib/Droidspaces/Containers/alpine/rootfs")
	off += recordPathMax
	copy(buf[off:off+recordNameMax], "alpine-host")
	off += recordNameMax
	copy(buf[off:off+inetAddrStrLen], "172.28.0.2")
	off += inetAddrStrLen
	copy(buf[off:off+recordPathMax], "/sbin/init")
	off += recordPathMax
	binary.BigEndian.PutUint32(buf[off:off+4], 1234)
	off += 4
	buf[off] = byte(NetNAT)
	off++
	buf[off] = 1
	off++
	off += 2
	binary.BigEndian.PutUint16(buf[off:off+2], 8080)
	binary.BigEndian.PutUint16(buf[off+4:off+6], 80)
	off += recordPortsMax * portRecordSize
	binary.BigEndian.PutUint64(buf[off:off+8], 1710000000)

	container, err := parseContainerRecord(buf)
	if err != nil {
		t.Fatal(err)
	}
	if container.Name != "alpine" || !container.Running || container.PID != 1234 {
		t.Fatalf("unexpected container: %#v", container)
	}
	if container.NetMode != "nat" || container.NATIP != "172.28.0.2" {
		t.Fatalf("unexpected network data: %#v", container)
	}
	if len(container.Ports) != 1 || container.Ports[0].HostPort != 8080 || container.Ports[0].ContainerPort != 80 {
		t.Fatalf("unexpected ports: %#v", container.Ports)
	}
}
