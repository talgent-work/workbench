package nativeprocess

import (
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// Retain current native facts independently of best-effort feedback delivery.
// These describe this process only; they are reset when another process starts.
func (s *ProtocolState) ObservePhase(sequence int64, observations []command.ExecutionObservation) {
	for _, observation := range observations {
		switch observation.Kind {
		case command.ExecutionTurnStarted, command.ExecutionTurnFinished:
			s.Turn = application.TurnObservation{Sequence: sequence, Active: observation.Kind == command.ExecutionTurnStarted, Completed: observation.TurnCompleted}
			s.PhaseSequence = sequence
		case command.ExecutionUserInteractionRequired:
			s.PhaseSequence = sequence
		case command.ExecutionUserInteractionResolved:
			s.PhaseSequence = sequence
		}
	}
}

// Observation answers ObserveProcess for the running process from memory.
func (s *ProtocolState) Observation() application.ProcessObservation {
	return application.ProcessObservation{
		State: application.ProcessRunning, Ready: s.Ready && s.Failure == "",
		Turn: s.Turn, PhaseSequence: s.PhaseSequence, RequiredInteractionIDs: s.PendingInteractions(),
	}
}
