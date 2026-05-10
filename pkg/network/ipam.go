package network

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	maxSubnetSize = 1 << 16
)

type ipamState struct {
	Subnets map[string]string `json:"subnets"`
}

// AllocateIP 在给定子网中找一个空闲 IP 并标记为已用,持久化到磁盘
func AllocateIP(subnet *net.IPNet) (net.IP, error) {
	if err := ensureLayout(); err != nil {
		return nil, err
	}
	if err := validateSubnet(subnet); err != nil {
		return nil, err
	}

	var allocatedIP net.IP
	if err := withIpamLocked(func(s *ipamState) error {
		key := subnet.String()
		bitmap, ok := s.Subnets[key]
		size, err := subnetSize(subnet)
		if err != nil {
			return err
		}
		if !ok {
			bitmap = strings.Repeat("0", size)
			// 默认占住网络地址(0)和广播(size-1)
			bitmap = setBit(bitmap, 0)
			bitmap = setBit(bitmap, size-1)
		}
		idx := -1
		for i := 1; i < size-1; i++ {
			if bitmap[i] == '0' {
				idx = i
				break
			}
		}
		if idx < 0 {
			return errors.New("no free IP in subnet")
		}
		bitmap = setBit(bitmap, idx)
		s.Subnets[key] = bitmap
		allocatedIP = ipFromIndex(subnet, idx)
		return nil
	}); err != nil {
		return nil, err
	}
	return allocatedIP, nil
}

// ReserveIP 标记某个具体 IP 为已使用(用于网关)。已占用则返回 nil(幂等)
func ReserveIP(subnet *net.IPNet, ip net.IP) error {
	if err := ensureLayout(); err != nil {
		return err
	}
	if err := validateSubnet(subnet); err != nil {
		return err
	}
	return withIpamLocked(func(s *ipamState) error {
		key := subnet.String()
		size, err := subnetSize(subnet)
		if err != nil {
			return err
		}
		bitmap, ok := s.Subnets[key]
		if !ok {
			bitmap = strings.Repeat("0", size)
			bitmap = setBit(bitmap, 0)
			bitmap = setBit(bitmap, size-1)
		}
		idx, err := indexFromIP(subnet, ip)
		if err != nil {
			return err
		}
		bitmap = setBit(bitmap, idx)
		s.Subnets[key] = bitmap
		return nil
	})
}

// ReleaseIP 把某个 IP 标记为空闲
func ReleaseIP(subnet *net.IPNet, ip net.IP) error {
	if err := ensureLayout(); err != nil {
		return err
	}
	if err := validateSubnet(subnet); err != nil {
		return err
	}
	return withIpamLocked(func(s *ipamState) error {
		key := subnet.String()
		bitmap, ok := s.Subnets[key]
		if !ok {
			return nil
		}
		idx, err := indexFromIP(subnet, ip)
		if err != nil {
			return err
		}
		if idx < 0 || idx >= len(bitmap) {
			return nil
		}
		bitmap = clearBit(bitmap, idx)
		s.Subnets[key] = bitmap
		return nil
	})
}

func validateSubnet(subnet *net.IPNet) error {
	if subnet == nil || subnet.IP.To4() == nil {
		return errors.New("only IPv4 subnets are supported")
	}
	ones, _ := subnet.Mask.Size()
	if ones >= 31 {
		return fmt.Errorf("subnet prefix /%d is too small", ones)
	}
	size, err := subnetSize(subnet)
	if err != nil {
		return err
	}
	if size > maxSubnetSize {
		return fmt.Errorf("subnet larger than /%d not supported", 32-bitsForSize(maxSubnetSize))
	}
	return nil
}

func bitsForSize(n int) int {
	bits := 0
	for n > 1 {
		n >>= 1
		bits++
	}
	return bits
}

func subnetSize(subnet *net.IPNet) (int, error) {
	ones, bits := subnet.Mask.Size()
	if bits != 32 {
		return 0, errors.New("only IPv4 subnets are supported")
	}
	return 1 << (bits - ones), nil
}

func ipFromIndex(subnet *net.IPNet, idx int) net.IP {
	base := binary.BigEndian.Uint32(subnet.IP.To4())
	val := base + uint32(idx)
	out := make(net.IP, 4)
	binary.BigEndian.PutUint32(out, val)
	return out
}

func indexFromIP(subnet *net.IPNet, ip net.IP) (int, error) {
	v4 := ip.To4()
	if v4 == nil {
		return 0, errors.New("only IPv4 is supported")
	}
	base := binary.BigEndian.Uint32(subnet.IP.To4())
	target := binary.BigEndian.Uint32(v4)
	if target < base {
		return 0, fmt.Errorf("ip %s outside subnet %s", ip, subnet)
	}
	idx := int(target - base)
	size, err := subnetSize(subnet)
	if err != nil {
		return 0, err
	}
	if idx >= size {
		return 0, fmt.Errorf("ip %s outside subnet %s", ip, subnet)
	}
	return idx, nil
}

func setBit(bitmap string, idx int) string {
	b := []byte(bitmap)
	b[idx] = '1'
	return string(b)
}

func clearBit(bitmap string, idx int) string {
	b := []byte(bitmap)
	b[idx] = '0'
	return string(b)
}

// withIpamLocked 用 flock 包住 read-modify-write
func withIpamLocked(fn func(*ipamState) error) error {
	path := ipamFilePath()
	if err := os.MkdirAll(dataRoot(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock ipam: %w", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	state := ipamState{Subnets: map[string]string{}}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode ipam: %w", err)
		}
		if state.Subnets == nil {
			state.Subnets = map[string]string{}
		}
	}
	if err := fn(&state); err != nil {
		return err
	}
	out, err := json.MarshalIndent(&state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.Truncate(path, 0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	if _, err := f.Write(out); err != nil {
		return err
	}
	return f.Sync()
}
