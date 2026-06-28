package socketd

import (
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	backendSocketName = "\x00droidspaces-socketd-backend"
	protocolMagic     = 0x44534150
	protocolVersion   = 1
	maxPayload        = 1024 * 1024

	recordNameMax       = 256
	recordPathMax       = 1024
	recordPortsMax      = 16
	uuidLen             = 32
	inetAddrStrLen      = 16
	inspectEnvMax       = 32
	inspectBindsMax     = 32
	inspectStringMax    = 1024
	portRecordSize      = 12
	containerRecordSize = 2817
	inspectRecordSize   = 111553
	eventRecordSize     = 369
)

type opcode uint16

const (
	opPing             opcode = 1
	opCapabilities     opcode = 2
	opInfo             opcode = 3
	opListContainers   opcode = 4
	opInspectContainer opcode = 5
	opStartContainer   opcode = 6
	opStopContainer    opcode = 7
	opRestartContainer opcode = 8
	opListImages       opcode = 9
	opPollEvents       opcode = 10
)

type Status uint16

const (
	StatusOK             Status = 0
	StatusBadRequest     Status = 1
	StatusUnsupported    Status = 2
	StatusNotFound       Status = 3
	StatusInternalError  Status = 4
	StatusForbidden      Status = 5
	StatusAlreadyRunning Status = 6
	StatusAlreadyStopped Status = 7
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusBadRequest:
		return "bad request"
	case StatusUnsupported:
		return "unsupported"
	case StatusNotFound:
		return "not found"
	case StatusInternalError:
		return "internal error"
	case StatusForbidden:
		return "forbidden"
	case StatusAlreadyRunning:
		return "already running"
	case StatusAlreadyStopped:
		return "already stopped"
	default:
		return fmt.Sprintf("status %d", uint16(s))
	}
}

type StatusError struct {
	Status Status
}

func (e StatusError) Error() string {
	return "socketd backend returned " + e.Status.String()
}

type NetMode uint8

const (
	NetHost    NetMode = 0
	NetNAT     NetMode = 1
	NetNone    NetMode = 2
	NetGateway NetMode = 3
)

func (m NetMode) String() string {
	switch m {
	case NetHost:
		return "host"
	case NetNAT:
		return "nat"
	case NetNone:
		return "none"
	case NetGateway:
		return "gateway"
	default:
		return fmt.Sprintf("mode-%d", uint8(m))
	}
}

type Port struct {
	HostPort         uint16 `json:"hostPort"`
	HostPortEnd      uint16 `json:"hostPortEnd,omitempty"`
	ContainerPort    uint16 `json:"containerPort"`
	ContainerPortEnd uint16 `json:"containerPortEnd,omitempty"`
	Protocol         string `json:"protocol"`
}

type Container struct {
	Name       string `json:"name"`
	UUID       string `json:"uuid,omitempty"`
	RootFSPath string `json:"rootfsPath,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	NATIP      string `json:"natIp,omitempty"`
	CustomInit string `json:"customInit,omitempty"`
	PID        int32  `json:"pid"`
	NetMode    string `json:"netMode"`
	Running    bool   `json:"running"`
	Ports      []Port `json:"ports,omitempty"`
	StartedAt  int64  `json:"startedAt,omitempty"`
}

type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type BindMount struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"readOnly"`
}

type Inspect struct {
	Container
	ImageRef          string      `json:"imageRef,omitempty"`
	DNSServers        string      `json:"dnsServers,omitempty"`
	MemoryLimit       int64       `json:"memoryLimit"`
	CPUQuota          int64       `json:"cpuQuota"`
	CPUPeriod         int64       `json:"cpuPeriod"`
	PidsLimit         int64       `json:"pidsLimit"`
	PrivilegedMask    int32       `json:"privilegedMask"`
	Foreground        bool        `json:"foreground"`
	VolatileMode      bool        `json:"volatileMode"`
	ForceCgroupV1     bool        `json:"forceCgroupV1"`
	DisableIPv6       bool        `json:"disableIPv6"`
	AndroidStorage    bool        `json:"androidStorage"`
	SELinuxPermissive bool        `json:"selinuxPermissive"`
	HWAccess          bool        `json:"hwAccess"`
	GPUMode           bool        `json:"gpuMode"`
	TermuxX11         bool        `json:"termuxX11"`
	BlockNestedNS     bool        `json:"blockNestedNamespaces"`
	IsImageMount      bool        `json:"isImageMount"`
	Env               []EnvVar    `json:"env,omitempty"`
	EnvTotal          int         `json:"envTotal"`
	Binds             []BindMount `json:"binds,omitempty"`
	BindTotal         int         `json:"bindTotal"`
	PortTotal         int         `json:"portTotal"`
}

type Info struct {
	ContainersTotal   uint32 `json:"containersTotal"`
	ContainersRunning uint32 `json:"containersRunning"`
	ContainersStopped uint32 `json:"containersStopped"`
}

type Event struct {
	Time      int64  `json:"time"`
	TimeNano  int64  `json:"timeNano"`
	Type      string `json:"type"`
	Action    string `json:"action"`
	ActorID   string `json:"actorId,omitempty"`
	ActorName string `json:"actorName,omitempty"`
}

func fixedStringBytes(value string, size int) []byte {
	buf := make([]byte, size)
	copy(buf, value)
	return buf
}

func readCString(buf []byte) string {
	if i := strings.IndexByte(string(buf), 0); i >= 0 {
		return string(buf[:i])
	}
	return string(buf)
}

func beInt32(buf []byte) int32 {
	return int32(binary.BigEndian.Uint32(buf))
}

func beInt64(buf []byte) int64 {
	return int64(binary.BigEndian.Uint64(buf))
}

func parsePortRecord(buf []byte) Port {
	proto := "tcp"
	if buf[8] == 1 {
		proto = "udp"
	}
	return Port{
		HostPort:         binary.BigEndian.Uint16(buf[0:2]),
		HostPortEnd:      binary.BigEndian.Uint16(buf[2:4]),
		ContainerPort:    binary.BigEndian.Uint16(buf[4:6]),
		ContainerPortEnd: binary.BigEndian.Uint16(buf[6:8]),
		Protocol:         proto,
	}
}

func parseContainerRecord(buf []byte) (Container, error) {
	if len(buf) != containerRecordSize {
		return Container{}, fmt.Errorf("invalid container record size: %d", len(buf))
	}

	var out Container
	off := 0
	out.Name = readCString(buf[off : off+recordNameMax])
	off += recordNameMax
	out.UUID = readCString(buf[off : off+uuidLen+1])
	off += uuidLen + 1
	out.RootFSPath = readCString(buf[off : off+recordPathMax])
	off += recordPathMax
	out.Hostname = readCString(buf[off : off+recordNameMax])
	off += recordNameMax
	out.NATIP = readCString(buf[off : off+inetAddrStrLen])
	off += inetAddrStrLen
	out.CustomInit = readCString(buf[off : off+recordPathMax])
	off += recordPathMax
	out.PID = beInt32(buf[off : off+4])
	off += 4
	mode := NetMode(buf[off])
	out.NetMode = mode.String()
	off++
	portCount := int(buf[off])
	off++
	off += 2

	if portCount > recordPortsMax {
		portCount = recordPortsMax
	}
	for i := 0; i < portCount; i++ {
		start := off + i*portRecordSize
		out.Ports = append(out.Ports, parsePortRecord(buf[start:start+portRecordSize]))
	}
	off += recordPortsMax * portRecordSize
	out.StartedAt = beInt64(buf[off : off+8])
	out.Running = out.PID > 0
	return out, nil
}

func parseInspectRecord(buf []byte) (Inspect, error) {
	if len(buf) != inspectRecordSize {
		return Inspect{}, fmt.Errorf("invalid inspect record size: %d", len(buf))
	}

	var out Inspect
	off := 0
	recordVersion := binary.BigEndian.Uint16(buf[off : off+2])
	off += 2
	off += 2
	recordSize := binary.BigEndian.Uint32(buf[off : off+4])
	off += 4
	if recordVersion != 1 || recordSize != inspectRecordSize {
		return Inspect{}, fmt.Errorf("unsupported inspect record version %d size %d", recordVersion, recordSize)
	}

	out.Name = readCString(buf[off : off+recordNameMax])
	off += recordNameMax
	out.UUID = readCString(buf[off : off+uuidLen+1])
	off += uuidLen + 1
	out.RootFSPath = readCString(buf[off : off+recordPathMax])
	off += recordPathMax
	out.ImageRef = readCString(buf[off : off+recordPathMax])
	off += recordPathMax
	out.Hostname = readCString(buf[off : off+recordNameMax])
	off += recordNameMax
	out.NATIP = readCString(buf[off : off+inetAddrStrLen])
	off += inetAddrStrLen
	out.CustomInit = readCString(buf[off : off+recordPathMax])
	off += recordPathMax
	out.DNSServers = readCString(buf[off : off+inspectStringMax])
	off += inspectStringMax

	out.PID = beInt32(buf[off : off+4])
	off += 4
	out.StartedAt = beInt64(buf[off : off+8])
	off += 8
	out.MemoryLimit = beInt64(buf[off : off+8])
	off += 8
	out.CPUQuota = beInt64(buf[off : off+8])
	off += 8
	out.CPUPeriod = beInt64(buf[off : off+8])
	off += 8
	out.PidsLimit = beInt64(buf[off : off+8])
	off += 8
	out.PrivilegedMask = beInt32(buf[off : off+4])
	off += 4

	out.NetMode = NetMode(buf[off]).String()
	off++
	out.Foreground = buf[off] != 0
	off++
	out.VolatileMode = buf[off] != 0
	off++
	out.ForceCgroupV1 = buf[off] != 0
	off++
	out.DisableIPv6 = buf[off] != 0
	off++
	out.AndroidStorage = buf[off] != 0
	off++
	out.SELinuxPermissive = buf[off] != 0
	off++
	out.HWAccess = buf[off] != 0
	off++
	out.GPUMode = buf[off] != 0
	off++
	out.TermuxX11 = buf[off] != 0
	off++
	out.BlockNestedNS = buf[off] != 0
	off++
	out.IsImageMount = buf[off] != 0
	off++

	envCount := int(binary.BigEndian.Uint16(buf[off : off+2]))
	off += 2
	out.EnvTotal = int(binary.BigEndian.Uint16(buf[off : off+2]))
	off += 2
	bindCount := int(binary.BigEndian.Uint16(buf[off : off+2]))
	off += 2
	out.BindTotal = int(binary.BigEndian.Uint16(buf[off : off+2]))
	off += 2
	portCount := int(binary.BigEndian.Uint16(buf[off : off+2]))
	off += 2
	out.PortTotal = int(binary.BigEndian.Uint16(buf[off : off+2]))
	off += 2

	if envCount > inspectEnvMax {
		envCount = inspectEnvMax
	}
	for i := 0; i < inspectEnvMax; i++ {
		key := readCString(buf[off : off+recordNameMax])
		off += recordNameMax
		value := readCString(buf[off : off+inspectStringMax])
		off += inspectStringMax
		if i < envCount {
			out.Env = append(out.Env, EnvVar{Key: key, Value: value})
		}
	}

	if bindCount > inspectBindsMax {
		bindCount = inspectBindsMax
	}
	for i := 0; i < inspectBindsMax; i++ {
		source := readCString(buf[off : off+recordPathMax])
		off += recordPathMax
		dest := readCString(buf[off : off+recordPathMax])
		off += recordPathMax
		readOnly := buf[off] != 0
		off += 4
		if i < bindCount {
			out.Binds = append(out.Binds, BindMount{
				Source:      source,
				Destination: dest,
				ReadOnly:    readOnly,
			})
		}
	}

	if portCount > recordPortsMax {
		portCount = recordPortsMax
	}
	for i := 0; i < recordPortsMax; i++ {
		port := parsePortRecord(buf[off : off+portRecordSize])
		off += portRecordSize
		if i < portCount {
			out.Ports = append(out.Ports, port)
		}
	}
	out.Running = out.PID > 0
	return out, nil
}

func parseEventRecord(buf []byte) (Event, error) {
	if len(buf) != eventRecordSize {
		return Event{}, fmt.Errorf("invalid event record size: %d", len(buf))
	}
	off := 0
	out := Event{}
	out.Time = beInt64(buf[off : off+8])
	off += 8
	out.TimeNano = beInt64(buf[off : off+8])
	off += 8
	out.Type = readCString(buf[off : off+32])
	off += 32
	out.Action = readCString(buf[off : off+32])
	off += 32
	out.ActorID = readCString(buf[off : off+uuidLen+1])
	off += uuidLen + 1
	out.ActorName = readCString(buf[off : off+recordNameMax])
	return out, nil
}
