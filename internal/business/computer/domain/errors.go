package domain

import (
	"errors"

	"github.com/go-playground/validator/v10"
)

var domainValidator = validator.New()

var (
	ErrInvalidComputer        = errors.New("computer: invalid computer")
	ErrInvalidDesk            = errors.New("computer: invalid desk")
	ErrComputerNotFound       = errors.New("computer: computer not found")
	ErrDeskNotFound           = errors.New("computer: desk not found")
	ErrDeskTaken              = errors.New("computer: desk identity belongs to another run")
	ErrCapacityBelowOccupied  = errors.New("computer: capacity cannot be set below the room occupied now")
	ErrConcurrentModification = errors.New("computer: concurrent modification")
	ErrCredentialRejected     = errors.New("computer: computer credential rejected")
	ErrComputerRemoved        = errors.New("computer: computer removed")
	ErrRotationUnderWay       = errors.New("computer: a rotated credential is reserved for another connection")
)
