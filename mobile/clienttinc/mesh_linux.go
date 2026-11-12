//go:build linux && !android

package clienttinc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
)

// This sampling input contains public names and assignment identity only.
// Client keys, VLESS UUIDs, credentials and certificate material never enter it.
type meshInput struct {
	self       string
	peers      []string
	egress     MeshEgress
	generation uint64
}

func publicMeshInput(c wire.ClientConfig) *meshInput {
	m := &meshInput{self: c.Self.Name, egress: MeshEgress{ID: c.Assignment.EgressID.String(), Country: c.Assignment.Country}}
	for _, p := range c.Ingresses {
		m.peers = append(m.peers, p.Name)
	}
	return m
}

func (c *controller) setMeshInput(input *meshInput) {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if input == nil && c.meshInput == nil {
		return
	}
	if old := c.meshInput; old != nil && input != nil && old.self == input.self && old.egress == input.egress && slices.Equal(old.peers, input.peers) {
		return
	}
	c.meshGeneration++
	c.meshInput, c.meshObservation, c.status.Mesh = nil, nil, nil
	if input == nil {
		c.status.Ingresses = nil
		return
	}
	value := *input
	value.peers = append([]string(nil), input.peers...)
	value.generation = c.meshGeneration
	c.meshInput = &value
	select {
	case c.meshWake <- struct{}{}:
	default:
	}
}

func (c *controller) meshSnapshot() *meshInput {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.meshInput == nil {
		return nil
	}
	v := *c.meshInput
	v.peers = append([]string(nil), v.peers...)
	return &v
}

func (c *controller) publishMesh(input *meshInput, observation *MeshStatus) {
	// Serialize callbacks with ordinary lifecycle events. Never keep this lock
	// during a native query or counter read. OnStatus must return promptly.
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	c.mu.Lock()
	if c.meshInput == nil || c.meshInput.generation != input.generation || c.status.Country != input.egress.Country || c.status.EgressID != input.egress.ID {
		c.mu.Unlock()
		return
	}
	c.meshObservation = cloneMesh(observation)
	c.status.Mesh = cloneMesh(observation)
	if observation.ObservationState == "ok" {
		c.status.Ingresses = nil
		for _, p := range observation.Ingresses {
			if p.Connected != nil && *p.Connected {
				c.status.Ingresses = append(c.status.Ingresses, p.Name)
			}
		}
	}
	status := cloneStatus(c.status)
	c.mu.Unlock()
	// Sampling never changes state, health, grant expiry, country or permission.
	if c.onStatus != nil {
		c.onStatus(status)
	}
}

type meshQuery func(context.Context, string, ...string) ([]byte, error)

func collectMesh(ctx context.Context, ctl, dir string, input *meshInput, query meshQuery, traffic *ownedTraffic) *MeshStatus {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	m := &MeshStatus{ObservationState: "unavailable", Egress: input.egress}
	for _, name := range input.peers {
		m.Ingresses = append(m.Ingresses, MeshIngress{Name: name})
	}
	args := []string{"-c", dir, "--pidfile=" + filepath.Join(dir, "tinc.pid"), "dump"}
	connections, e := query(ctx, ctl, append(args, "connections")...)
	if e == nil {
		edges, e := query(ctx, ctl, append(args, "edges")...)
		if e == nil && ctx.Err() == nil {
			config := wire.ClientConfig{Self: wire.ClientIdentity{Name: input.self}}
			for _, name := range input.peers {
				config.Ingresses = append(config.Ingresses, wire.ClientIngress{Name: name})
			}
			connected := connectedIngresses(string(connections), string(edges), config)
			m.ObservationState = "ok"
			for i := range m.Ingresses {
				value := slices.Contains(connected, m.Ingresses[i].Name)
				m.Ingresses[i].Connected = &value
			}
		}
	}
	m.ObservedAt = time.Now().UTC()
	m.Traffic = readOwnedTraffic(traffic, m.ObservedAt, net.InterfaceByName, os.ReadFile)
	return m
}

type ownedTraffic struct {
	name  string
	index int
	epoch string
}

func ownTraffic(d *tunDevice) *ownedTraffic {
	if d == nil || d.file == nil || d.name != "qtc0" {
		return nil
	}
	i, err := net.InterfaceByName(d.name)
	if err != nil {
		return nil
	}
	return &ownedTraffic{name: d.name, index: i.Index, epoch: strconv.FormatInt(time.Now().UnixNano(), 10)}
}

func readOwnedTraffic(source *ownedTraffic, now time.Time, lookup func(string) (*net.Interface, error), read func(string) ([]byte, error)) *MeshTraffic {
	if source == nil || source.name != "qtc0" || source.index <= 0 {
		return nil
	}
	i, err := lookup(source.name)
	if err != nil || i.Index != source.index {
		return nil
	}
	values := make([]uint64, 2)
	for n, counter := range []string{"rx_bytes", "tx_bytes"} {
		b, err := read("/sys/class/net/" + source.name + "/statistics/" + counter)
		if err != nil || len(b) > 32 {
			return nil
		}
		values[n], err = strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			return nil
		}
	}
	i, err = lookup(source.name)
	if err != nil || i.Index != source.index {
		return nil
	}
	return &MeshTraffic{Source: "system_tun", RXBytes: values[0], TXBytes: values[1], Epoch: source.epoch, SampledAt: now}
}

func (c *controller) startMeshSampler(ctx context.Context, ctl, dir string, system *tunDevice) func() {
	return c.runMeshSampler(ctx, 2*time.Second, func(ctx context.Context, input *meshInput, traffic *ownedTraffic) *MeshStatus {
		return collectMesh(ctx, ctl, dir, input, func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return command(ctx, "", name, args...)
		}, traffic)
	}, ownTraffic(system))
}

func (c *controller) runMeshSampler(ctx context.Context, interval time.Duration, collect func(context.Context, *meshInput, *ownedTraffic) *MeshStatus, traffic *ownedTraffic) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	c.mu.Lock()
	c.meshWake = wake
	c.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-ticker.C:
			}
			input := c.meshSnapshot()
			if input == nil {
				continue
			}
			observation := collect(ctx, input, traffic)
			if ctx.Err() != nil {
				return
			}
			c.publishMesh(input, observation)
		}
	}()
	return func() { cancel(); <-done }
}
