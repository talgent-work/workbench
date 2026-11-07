package infrastructure_test

// workRef keeps a started Work addressable after a start command that returns
// only whether it was admitted; a Work has at most one Harness process.
func workRef(workID string) func(bool, error) (string, error) {
	return func(_ bool, err error) (string, error) { return workID, err }
}

func continuedRef(workID string, err error) (string, error) { return workID, err }
