package connectrpc

import (
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// workCallerFromProto maps a Work Caller as its sender gave it; the domain
// validates it.
func workCallerFromProto(value *agentpublicv1.WorkCaller) domain.WorkCaller {
	return domain.WorkCaller{Kind: value.GetKind(), ID: value.GetId()}
}

// optionalWorkCaller maps a Work Caller condition; an absent one selects
// nothing by Work Caller.
func optionalWorkCaller(value *agentpublicv1.WorkCaller) *domain.WorkCaller {
	if value == nil {
		return nil
	}
	caller := workCallerFromProto(value)
	return &caller
}

// workCallerToProto maps a Work's Work Caller.
func workCallerToProto(value domain.WorkCaller) *agentpublicv1.WorkCaller {
	return &agentpublicv1.WorkCaller{Kind: value.Kind, Id: value.ID}
}

// workKeyFromProto maps an optional Work Key; an absent one is none.
func workKeyFromProto(value *agentpublicv1.WorkKey) domain.WorkKey {
	return domain.WorkKey{Kind: value.GetKind(), ID: value.GetId()}
}

// workKeyToProto omits a Work's absent Work Key.
func workKeyToProto(value domain.WorkKey) *agentpublicv1.WorkKey {
	if !value.Present() {
		return nil
	}
	return &agentpublicv1.WorkKey{Kind: value.Kind, Id: value.ID}
}

// fileHomeFromProto maps an optional File Home; an absent one is none.
func fileHomeFromProto(value *agentpublicv1.FileHome) domain.FileHome {
	return domain.FileHome{Kind: value.GetKind(), ID: value.GetId()}
}

// fileHomeToProto omits a Work's absent File Home.
func fileHomeToProto(value domain.FileHome) *agentpublicv1.FileHome {
	if !value.Present() {
		return nil
	}
	return &agentpublicv1.FileHome{Kind: value.Kind, Id: value.ID}
}
