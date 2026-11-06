package infrastructure

import (
	"encoding/json"
	"time"

	"github.com/talgent-work/workbench/internal/business/computer/domain"
)

func roomToRecord(room domain.Room) roomRecord {
	return roomRecord{MilliCPU: room.MilliCPU, MemoryBytes: room.MemoryBytes, Desks: room.Desks}
}

func roomFromRecord(record roomRecord) domain.Room {
	return domain.Room{MilliCPU: record.MilliCPU, MemoryBytes: record.MemoryBytes, Desks: record.Desks}
}

func encodeRoom(room domain.Room) (string, error) {
	raw, err := json.Marshal(roomToRecord(room))
	return string(raw), err
}

func decodeRoom(raw string) (domain.Room, error) {
	var record roomRecord
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return domain.Room{}, err
	}
	return roomFromRecord(record), nil
}

func millis(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.UnixMilli()
}

func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func computerToDO(computer *domain.Computer) (*computerDO, error) {
	capacity, err := encodeRoom(computer.Capacity)
	if err != nil {
		return nil, err
	}
	occupied, err := encodeRoom(computer.Occupied)
	if err != nil {
		return nil, err
	}
	line := make([]waitingDeskRecord, 0, len(computer.WaitingLine))
	for _, waiting := range computer.WaitingLine {
		line = append(line, waitingDeskRecord{DeskID: waiting.DeskID, Demand: roomToRecord(waiting.Demand)})
	}
	waitingLine, err := json.Marshal(line)
	if err != nil {
		return nil, err
	}
	return &computerDO{
		ID: computer.ID, Kind: string(computer.Kind), OwnerID: computer.OwnerID, Name: computer.Name,
		Capacity: capacity, Occupied: occupied, AdmissionPolicy: string(computer.Policy), WaitingLine: string(waitingLine),
		Reachable: computer.Presence.Reachable, UnreachableSince: millis(computer.Presence.UnreachableSince),
		TalgentComputerVersion: computer.Versions.TalgentComputer, ClaudeCodeVersion: computer.Versions.ClaudeCode, ClaudeCodeSandbox: computer.Sandbox,
		CredentialDigest: computer.Credential.Digest, CredentialExpiresAt: millis(computer.Credential.ExpiresAt),
		PendingDigest: computer.PendingCredential.Digest, PendingExpiresAt: millis(computer.PendingCredential.ExpiresAt), PendingIssuedAt: millis(computer.PendingCredential.IssuedAt),
		RetiredDigest: computer.RetiredCredential.Digest, RetiredExpiresAt: millis(computer.RetiredCredential.ExpiresAt),
		State: string(computer.State), Version: computer.Version,
	}, nil
}

// computerRoom is a Computer row's room: its Capacity, Occupied and Waiting
// Line.
type computerRoom struct {
	capacity, occupied domain.Room
	waitingLine        []domain.WaitingDesk
}

func roomFromDO(row *computerDO) (computerRoom, error) {
	capacity, err := decodeRoom(row.Capacity)
	if err != nil {
		return computerRoom{}, err
	}
	occupied, err := decodeRoom(row.Occupied)
	if err != nil {
		return computerRoom{}, err
	}
	var line []waitingDeskRecord
	if err := json.Unmarshal([]byte(row.WaitingLine), &line); err != nil {
		return computerRoom{}, err
	}
	room := computerRoom{capacity: capacity, occupied: occupied}
	for _, waiting := range line {
		room.waitingLine = append(room.waitingLine, domain.WaitingDesk{DeskID: waiting.DeskID, Demand: roomFromRecord(waiting.Demand)})
	}
	return room, nil
}

func computerFromDO(row *computerDO) (*domain.Computer, error) {
	room, err := roomFromDO(row)
	if err != nil {
		return nil, err
	}
	computer := &domain.Computer{
		ID: row.ID, Kind: domain.Kind(row.Kind), OwnerID: row.OwnerID, Name: row.Name,
		Capacity: room.capacity, Occupied: room.occupied, WaitingLine: room.waitingLine, Policy: domain.AdmissionPolicy(row.AdmissionPolicy),
		Presence:          domain.Presence{Reachable: row.Reachable, UnreachableSince: fromMillis(row.UnreachableSince)},
		Versions:          domain.ReportedVersions{TalgentComputer: row.TalgentComputerVersion, ClaudeCode: row.ClaudeCodeVersion},
		Sandbox:           row.ClaudeCodeSandbox,
		Credential:        domain.Credential{Digest: row.CredentialDigest, ExpiresAt: fromMillis(row.CredentialExpiresAt)},
		PendingCredential: domain.Credential{Digest: row.PendingDigest, ExpiresAt: fromMillis(row.PendingExpiresAt), IssuedAt: fromMillis(row.PendingIssuedAt)},
		RetiredCredential: domain.Credential{Digest: row.RetiredDigest, ExpiresAt: fromMillis(row.RetiredExpiresAt)},
		State:             domain.State(row.State), Events: domain.NewEventCollection(), Version: row.Version,
	}
	if err := computer.Validate(); err != nil {
		return nil, err
	}
	return computer, nil
}

func deskToDO(desk *domain.Desk) (*deskDO, error) {
	demand, err := encodeRoom(desk.Demand)
	if err != nil {
		return nil, err
	}
	return &deskDO{ID: desk.ID, WorkID: desk.WorkID, ComputerID: desk.ComputerID, Demand: demand, State: string(desk.State), Since: millis(desk.Since), OpenedAt: millis(desk.OpenedAt), Version: desk.Version}, nil
}

func deskFromDO(row *deskDO) (*domain.Desk, error) {
	demand, err := decodeRoom(row.Demand)
	if err != nil {
		return nil, err
	}
	desk := &domain.Desk{ID: row.ID, WorkID: row.WorkID, ComputerID: row.ComputerID, Demand: demand, State: domain.DeskState(row.State), Since: fromMillis(row.Since), OpenedAt: fromMillis(row.OpenedAt), Events: domain.NewEventCollection(), Version: row.Version}
	if err := desk.Validate(); err != nil {
		return nil, err
	}
	return desk, nil
}
