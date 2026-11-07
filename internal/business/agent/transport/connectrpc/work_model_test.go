package connectrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	agentpublicv1 "github.com/talgent-work/contracts/gen/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestAWorkModelSelectionCrossesTheContract(t *testing.T) {
	require.Equal(t, domain.WorkModelSelection{}, workModelFromProto(nil), "an absent selection leaves the default to the application")
	require.Equal(t, domain.WorkModelSelection{ConfigurationID: "glm-5"}, workModelFromProto(&agentpublicv1.WorkModelSelection{ConfigurationId: "glm-5"}))
	require.Equal(t, domain.MachineLoginModel(), workModelFromProto(&agentpublicv1.WorkModelSelection{MachineLogin: true}))
}

func TestAWorkShowsTheMachineLoginAsItsModel(t *testing.T) {
	work := workViewToProto(query.WorkView{WorkID: "work", WorkModel: domain.MachineLoginModel()})
	require.True(t, proto.Equal(&agentpublicv1.WorkModelSelection{MachineLogin: true}, work.GetModel()))
	require.Nil(t, work.GetModelOption(), "the machine's Claude login has no Model Configuration")

	option := query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "glm-5"}, Name: "GLM"}
	work = workViewToProto(query.WorkView{WorkID: "work", WorkModel: option.Selection, ModelOption: &option})
	require.True(t, proto.Equal(&agentpublicv1.WorkModelSelection{ConfigurationId: "glm-5"}, work.GetModel()))
	require.Equal(t, "GLM", work.GetModelOption().GetName())
}
