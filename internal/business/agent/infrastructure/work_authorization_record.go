package infrastructure

import (
	"encoding/json"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type workAuthorizationRecord struct {
	OwnerUserID    string                     `json:"owner_user_id"`
	Authorizations []workServiceAuthorization `json:"authorizations"`
}

type workServiceAuthorization struct {
	SupportedTargets []string `json:"supported_targets"`
	AuthorizationID  string   `json:"authorization_id"`
	Provider         string   `json:"provider"`
	Service          string   `json:"service"`
}

func encodeWorkAuthorization(selection domain.WorkAuthorizationSelection) (string, error) {
	record := workAuthorizationRecord{OwnerUserID: selection.OwnerUserID}
	for _, ref := range selection.Authorizations {
		record.Authorizations = append(record.Authorizations, workServiceAuthorization{
			AuthorizationID: ref.AuthorizationID, Provider: ref.Provider, Service: ref.Service, SupportedTargets: ref.SupportedTargets,
		})
	}
	return encode(record)
}

func decodeWorkAuthorization(data string) (domain.WorkAuthorizationSelection, error) {
	var record workAuthorizationRecord
	if err := json.Unmarshal([]byte(data), &record); err != nil {
		return domain.WorkAuthorizationSelection{}, oops.Wrap(err)
	}
	selection := domain.WorkAuthorizationSelection{OwnerUserID: record.OwnerUserID}
	for _, ref := range record.Authorizations {
		selection.Authorizations = append(selection.Authorizations, domain.WorkServiceAuthorization{
			AuthorizationID: ref.AuthorizationID, Provider: ref.Provider, Service: ref.Service, SupportedTargets: ref.SupportedTargets,
		})
	}
	return selection, selection.Validate()
}
