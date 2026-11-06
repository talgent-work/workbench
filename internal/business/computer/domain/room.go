package domain

import "fmt"

// Room is an amount of a Computer's room: compute for a Cloud, Desks for a
// Managed Computer. A Managed Computer's room is counted in Desks alone, so
// its compute dimensions stay zero.
type Room struct {
	MilliCPU    int64
	MemoryBytes int64
	Desks       int64
}

// Validate refuses a negative amount.
func (r Room) Validate() error {
	if r.MilliCPU < 0 || r.MemoryBytes < 0 || r.Desks < 0 {
		return fmt.Errorf("%w: room cannot be negative", ErrInvalidComputer)
	}
	return nil
}

func (r Room) add(other Room) Room {
	return Room{MilliCPU: r.MilliCPU + other.MilliCPU, MemoryBytes: r.MemoryBytes + other.MemoryBytes, Desks: r.Desks + other.Desks}
}

// sub takes other back from r; it refuses to give back more than r holds.
func (r Room) sub(other Room) (Room, error) {
	if !other.within(r) {
		return Room{}, fmt.Errorf("%w: a Desk gives back more room than is occupied", ErrInvalidComputer)
	}
	return Room{MilliCPU: r.MilliCPU - other.MilliCPU, MemoryBytes: r.MemoryBytes - other.MemoryBytes, Desks: r.Desks - other.Desks}, nil
}

// within reports whether every dimension of r fits within limit.
func (r Room) within(limit Room) bool {
	return r.MilliCPU <= limit.MilliCPU && r.MemoryBytes <= limit.MemoryBytes && r.Desks <= limit.Desks
}
