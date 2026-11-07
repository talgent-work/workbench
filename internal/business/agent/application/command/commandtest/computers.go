package commandtest

import (
	"context"
	"sync"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// Computers stands in for Computer beyond admission: it describes every
// Computer as one Cloud, or a Managed Computer while Managed, answers whether
// a Desk is seated and records the Desks that left. Admission itself fails
// loudly on the nil port it embeds.
type Computers struct {
	command.ComputerPort
	mu      sync.Mutex
	Managed bool
	Removed bool
	// Seated are the Desks seated, with when.
	Seated map[string]time.Time
	Left   []string
}

var _ command.ComputerPort = (*Computers)(nil)

func (c *Computers) DescribeComputer(context.Context, string) (command.ComputerDescription, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return command.ComputerDescription{Name: "Fixture Computer", Managed: c.Managed, Removed: c.Removed, Reachable: !c.Removed}, nil
}

func (c *Computers) DeskSeated(_ context.Context, deskID string) (time.Time, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, seated := c.Seated[deskID]
	return at, seated, nil
}

func (c *Computers) LeaveDesk(_ context.Context, deskID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Left = append(c.Left, deskID)
	return nil
}

func (c *Computers) ReleaseUnusedDesk(ctx context.Context, deskID string) {
	_ = c.LeaveDesk(ctx, deskID)
}

// Seat seats a Desk at a time.
func (c *Computers) Seat(deskID string, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Seated == nil {
		c.Seated = map[string]time.Time{}
	}
	c.Seated[deskID] = at
}
