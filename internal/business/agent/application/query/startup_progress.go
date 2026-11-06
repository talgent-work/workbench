package query

// StartupProgress is a presentation of committed preparation and startup
// facts. It never admits execution or owns a lifecycle transition.
type StartupProgress struct {
	RequestID, Revision, Status, Reason string
	Admitted                            bool
	Stages                              []StartupStage
}

type StartupStage struct{ Stage, Status string }

// StartupFacts contains only facts needed by the Work startup presentation.
// Revision is ordered across requests within one Session, not a message cursor.
type StartupFacts struct {
	RequestID, Revision, State, Reason                               string
	Initial, Admitted, SandboxReady, AgentReady                      bool
	InputPending                                                     bool
	InputPrepared, InputBuffered, Rejected, Recovering, EndRequested bool
}

func ProjectStartup(f StartupFacts) *StartupProgress {
	if f.RequestID == "" {
		return nil
	}
	p := &StartupProgress{RequestID: f.RequestID, Admitted: f.Admitted, Revision: f.Revision, Status: "preparing", Reason: f.Reason}
	type stage struct {
		name string
		done bool
	}
	// Repository checkout runs inside the environment before the Harness
	// starts, so the first start prepares its workspace between the
	// sandbox and the agent.
	stages := []stage{{"admission", f.Admitted}, {"sandbox", f.SandboxReady}}
	initial := f.Initial || f.InputPending
	if initial {
		stages = append(stages, stage{"workspace", f.InputPrepared})
	}
	stages = append(stages, stage{"agent", f.AgentReady})
	if initial {
		stages = append(stages, stage{"input", f.InputBuffered})
	}
	active := -1
	for i, s := range stages {
		status := "pending"
		if active < 0 && s.done {
			status = "completed"
		} else if active < 0 {
			active = i
			status = "active"
		}
		p.Stages = append(p.Stages, StartupStage{Stage: s.name, Status: status})
	}
	if active < 0 {
		p.Status = "completed"
		p.Reason = ""
		return p
	}
	switch {
	case f.Rejected || f.State == "failed":
		p.Status = "failed"
		p.Stages[active].Status = "failed"
	case f.EndRequested || f.State == "stopping" || f.State == "stopped" || f.State == "closing" || f.State == "closed":
		p.Status = "cancelled"
		p.Stages[active].Status = "cancelled"
	case f.Recovering:
		p.Status = "recovering"
	}
	return p
}
