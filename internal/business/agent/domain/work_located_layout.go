package domain

import "fmt"

// locateLayout fixes the Work's LocatedLayout and Agent Harness at creation
// (Locate layout): the definition's Work Layout applied to the Work's own
// facts, the Harness state mount placed by the Agent Harness.
func (s *AgentWork) locateLayout(layout WorkLayout, harnessID, startingUser string, managed bool) error {
	harness, err := AgentHarnessByID(harnessID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWork, err)
	}
	located, err := locateLayout(layout, harness, s.locatorFacts(startingUser), managed)
	if err != nil {
		return err
	}
	s.LocatedLayout, s.AgentHarnessReference = located, harness.ID
	return nil
}

// locatorFacts are the Work's values of the Locator conditions (Locator
// facts); a fact the Work lacks is left out, so the mounts following it
// locate nothing.
func (s *AgentWork) locatorFacts(startingUser string) LocatorFacts {
	facts := LocatorFacts{ConditionWork: s.ID, ConditionAgentDefinition: s.AgentID}
	if s.Subject.Present() {
		facts[ConditionSubject] = string(s.Subject.Kind) + ":" + s.Subject.ID
	}
	for condition, value := range map[LocatorCondition]string{ConditionPerson: s.RepresentedPerson, ConditionStarter: startingUser, ConditionProject: s.ProjectID} {
		if value != "" {
			facts[condition] = value
		}
	}
	return facts
}

// AgentHarness is the Agent Harness the Work runs.
func (s *AgentWork) AgentHarness() (AgentHarness, error) {
	return AgentHarnessByID(s.AgentHarnessReference)
}

// validateLocatedLayout keeps the fixed layout and Harness valid, and a
// Managed Work mounting no Directory.
func (s *AgentWork) validateLocatedLayout() error {
	if _, err := s.AgentHarness(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWork, err)
	}
	if s.ComputerKind == ComputerManaged && len(s.LocatedLayout.Mounts) > 0 {
		return fmt.Errorf("%w: a Work on a Managed Computer mounts no Directory", ErrInvalidWork)
	}
	return s.LocatedLayout.validate()
}
