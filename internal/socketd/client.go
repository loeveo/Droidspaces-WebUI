package socketd

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

type Client struct {
	Timeout time.Duration
}

func NewClient(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{Timeout: timeout}
}

func (c *Client) Ping(ctx context.Context) error {
	payload, err := c.request(ctx, opPing, nil)
	if err != nil {
		return err
	}
	if string(payload) != "PONG" {
		return fmt.Errorf("unexpected ping response %q", string(payload))
	}
	return nil
}

func (c *Client) Capabilities(ctx context.Context) (uint32, error) {
	payload, err := c.request(ctx, opCapabilities, nil)
	if err != nil {
		return 0, err
	}
	if len(payload) != 4 {
		return 0, fmt.Errorf("invalid capabilities payload size: %d", len(payload))
	}
	return binary.BigEndian.Uint32(payload), nil
}

func (c *Client) Info(ctx context.Context) (Info, error) {
	payload, err := c.request(ctx, opInfo, nil)
	if err != nil {
		return Info{}, err
	}
	if len(payload) != 12 {
		return Info{}, fmt.Errorf("invalid info payload size: %d", len(payload))
	}
	return Info{
		ContainersTotal:   binary.BigEndian.Uint32(payload[0:4]),
		ContainersRunning: binary.BigEndian.Uint32(payload[4:8]),
		ContainersStopped: binary.BigEndian.Uint32(payload[8:12]),
	}, nil
}

func (c *Client) ListContainers(ctx context.Context, includeAll bool) ([]Container, error) {
	payloadReq := []byte{0, 0, 0, 0}
	if includeAll {
		payloadReq[0] = 1
	}
	payload, err := c.request(ctx, opListContainers, payloadReq)
	if err != nil {
		return nil, err
	}
	if len(payload)%containerRecordSize != 0 {
		return nil, fmt.Errorf("invalid container list payload size: %d", len(payload))
	}
	containers := make([]Container, 0, len(payload)/containerRecordSize)
	for off := 0; off < len(payload); off += containerRecordSize {
		item, err := parseContainerRecord(payload[off : off+containerRecordSize])
		if err != nil {
			return nil, err
		}
		containers = append(containers, item)
	}
	return containers, nil
}

func (c *Client) InspectContainer(ctx context.Context, target string) (Inspect, error) {
	payload := fixedStringBytes(target, recordNameMax)
	resp, err := c.request(ctx, opInspectContainer, payload)
	if err != nil {
		return Inspect{}, err
	}
	return parseInspectRecord(resp)
}

func (c *Client) StartContainer(ctx context.Context, target string) error {
	return c.lifecycle(ctx, opStartContainer, target, -1)
}

func (c *Client) StopContainer(ctx context.Context, target string, timeoutSeconds int) error {
	return c.lifecycle(ctx, opStopContainer, target, timeoutSeconds)
}

func (c *Client) RestartContainer(ctx context.Context, target string, timeoutSeconds int) error {
	return c.lifecycle(ctx, opRestartContainer, target, timeoutSeconds)
}

func (c *Client) PollEvents(ctx context.Context, since int64) ([]Event, error) {
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, uint64(since))
	resp, err := c.request(ctx, opPollEvents, payload)
	if err != nil {
		return nil, err
	}
	if len(resp)%eventRecordSize != 0 {
		return nil, fmt.Errorf("invalid event payload size: %d", len(resp))
	}
	events := make([]Event, 0, len(resp)/eventRecordSize)
	for off := 0; off < len(resp); off += eventRecordSize {
		event, err := parseEventRecord(resp[off : off+eventRecordSize])
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func (c *Client) lifecycle(ctx context.Context, op opcode, target string, timeoutSeconds int) error {
	payload := make([]byte, recordNameMax+4)
	copy(payload, fixedStringBytes(target, recordNameMax))
	binary.BigEndian.PutUint32(payload[recordNameMax:], uint32(int32(timeoutSeconds)))
	_, err := c.request(ctx, op, payload)
	return err
}

func (c *Client) request(ctx context.Context, op opcode, payload []byte) ([]byte, error) {
	if len(payload) > maxPayload {
		return nil, fmt.Errorf("payload too large: %d", len(payload))
	}

	dialer := net.Dialer{Timeout: c.Timeout}
	conn, err := dialer.DialContext(ctx, "unix", backendSocketName)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(c.Timeout))
	}

	header := make([]byte, 12)
	binary.BigEndian.PutUint32(header[0:4], protocolMagic)
	binary.BigEndian.PutUint16(header[4:6], protocolVersion)
	binary.BigEndian.PutUint16(header[6:8], uint16(op))
	binary.BigEndian.PutUint32(header[8:12], uint32(len(payload)))

	if _, err := conn.Write(header); err != nil {
		return nil, err
	}
	if len(payload) > 0 {
		if _, err := conn.Write(payload); err != nil {
			return nil, err
		}
	}

	respHeader := make([]byte, 12)
	if _, err := io.ReadFull(conn, respHeader); err != nil {
		return nil, err
	}
	if binary.BigEndian.Uint32(respHeader[0:4]) != protocolMagic {
		return nil, fmt.Errorf("invalid response magic")
	}
	if binary.BigEndian.Uint16(respHeader[4:6]) != protocolVersion {
		return nil, fmt.Errorf("unsupported response version")
	}
	status := Status(binary.BigEndian.Uint16(respHeader[6:8]))
	payloadLen := binary.BigEndian.Uint32(respHeader[8:12])
	if payloadLen > maxPayload {
		return nil, fmt.Errorf("response payload too large: %d", payloadLen)
	}

	resp := make([]byte, payloadLen)
	if payloadLen > 0 {
		if _, err := io.ReadFull(conn, resp); err != nil {
			return nil, err
		}
	}
	if status != StatusOK {
		return nil, StatusError{Status: status}
	}
	return resp, nil
}
