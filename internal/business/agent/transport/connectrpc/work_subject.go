package connectrpc

import (
	agentprivatev1 "github.com/talgent-work/contracts/gen/agent/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// subjectFromProto maps an optional Work Subject; an absent one is none.
func subjectFromProto(value *agentprivatev1.WorkSubject) domain.WorkSubject {
	return domain.WorkSubject{Kind: domain.SubjectKind(value.GetKind()), ID: value.GetId()}
}

// subjectToProto omits a Work's absent Subject.
func subjectToProto(value domain.WorkSubject) *agentprivatev1.WorkSubject {
	if !value.Present() {
		return nil
	}
	return &agentprivatev1.WorkSubject{Kind: string(value.Kind), Id: value.ID}
}

func readOnlyMountsFromProto(values []*agentprivatev1.ReadOnlyMount) []domain.ReadOnlyMount {
	if len(values) == 0 {
		return nil
	}
	mounts := make([]domain.ReadOnlyMount, 0, len(values))
	for _, value := range values {
		mounts = append(mounts, domain.ReadOnlyMount{Name: value.GetName(), Reference: value.GetReference()})
	}
	return mounts
}
