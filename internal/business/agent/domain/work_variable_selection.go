package domain

import (
	"fmt"
	"slices"
)

// WorkVariableSelection names the Personal Variables a Work enabled at its
// creation, in name order and without their values. The Work keeps it on
// Resume and Restart, so a Personal Variable its person adds later stays out
// of it (Selection stability).
type WorkVariableSelection struct {
	Names []string
}

// Validate checks the selection as persisted: Personal Variable names, one
// each, in name order.
func (s WorkVariableSelection) Validate() error {
	for i, name := range s.Names {
		if err := (PersonalVariable{Name: name}).Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidWork, err)
		}
		if i > 0 && s.Names[i-1] >= name {
			return fmt.Errorf("%w: a Work Variable Selection names each variable once, in name order", ErrInvalidWork)
		}
	}
	return nil
}

// Clone copies the selection.
func (s WorkVariableSelection) Clone() WorkVariableSelection {
	return WorkVariableSelection{Names: slices.Clone(s.Names)}
}

// PersonalVariableChoice is a start's choice of the Personal Variables its
// new Work enables. A start that chooses none enables every Personal Variable
// its person keeps; a chosen name the person does not keep is ignored.
type PersonalVariableChoice struct {
	// Chosen reports that the start names the variables it enables; with no
	// Names it enables none.
	Chosen bool
	Names  []string
}

// WorkVariableInput is what a creation supplies to Determine variable
// selection: the names of the Personal Variables the represented person keeps
// now, from their PersonalVariableSet, and the start's choice.
type WorkVariableInput struct {
	Offered []string
	Choice  PersonalVariableChoice
}

// determineVariableSelection fixes the Personal Variables the new Work
// enables (Determine variable selection): every offered one unless the start
// chose, then the offered ones it chose (Variable selection at creation). A
// Work whose Agent represents no person enables none.
func (s *AgentWork) determineVariableSelection(input WorkVariableInput) error {
	var selection WorkVariableSelection
	if s.RepresentedPerson != "" {
		for _, name := range input.Offered {
			if !input.Choice.Chosen || slices.Contains(input.Choice.Names, name) {
				selection.Names = append(selection.Names, name)
			}
		}
	}
	if err := selection.Validate(); err != nil {
		return err
	}
	s.VariableSelection = selection
	return nil
}
