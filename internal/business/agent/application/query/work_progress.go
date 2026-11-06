package query

import "time"

type WorkProgress struct {
	Items     []WorkProgressItem
	UpdatedAt time.Time
}

type WorkProgressItem struct {
	Content, Status, ActiveForm string
}

// SubAgent is one SubAgent of the Work's current Harness process.
type SubAgent struct {
	ID, Type, Task, Status string
}
