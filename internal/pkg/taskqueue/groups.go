package taskqueue

import "fmt"

// A named queue's worker takes the processors, and its scheduler the periodic
// tasks, that business modules annotate with these tags; a tag naming another
// queue leaves them unregistered with no error.

// ProcessorTag tags a constructor that returns one processor for queue.
func ProcessorTag(queue string) string { return groupTag(processorGroup(queue), false) }

// ProcessorsTag tags a constructor that returns processors for queue as a slice.
func ProcessorsTag(queue string) string { return groupTag(processorGroup(queue), true) }

// PeriodicTaskTag tags a constructor that returns one periodic task for queue.
func PeriodicTaskTag(queue string) string { return groupTag(periodicTaskGroup(queue), false) }

// PeriodicTasksTag tags a constructor that returns periodic tasks for queue as a slice.
func PeriodicTasksTag(queue string) string { return groupTag(periodicTaskGroup(queue), true) }

func processorGroup(queue string) string    { return "taskqueue.processors." + queue }
func periodicTaskGroup(queue string) string { return "taskqueue.periodic_tasks." + queue }

func groupTag(group string, flatten bool) string {
	if flatten {
		group += ",flatten"
	}
	return fmt.Sprintf("group:%q", group)
}

// EnqueuerTag tags a parameter that takes queue's client, for a business
// module enqueuing on more than the one queue Bind selects.
func EnqueuerTag(queue string) string { return fmt.Sprintf(`name:%q`, queue) }
