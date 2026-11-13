package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

const deskKey = "Bearer desk-key-for-ticket-42"

func deskTools() domain.ApplicationMCPEndpoint {
	return domain.ApplicationMCPEndpoint{Name: "tickets", URL: "https://desk.example/mcp", Headers: map[string]string{"Authorization": deskKey}}
}

// A malformed Application MCP endpoint is refused, and the refusal never
// carries its header values.
func TestAnApplicationEndpointIsWellFormed(t *testing.T) {
	require.NoError(t, deskTools().Validate())
	require.NoError(t, (domain.ApplicationMCPEndpoint{Name: "Desk_2", URL: "http://desk.example:8080/mcp"}).Validate(), "headers are optional")

	for name, change := range map[string]func(*domain.ApplicationMCPEndpoint){
		"empty name":        func(e *domain.ApplicationMCPEndpoint) { e.Name = "" },
		"leading dash":      func(e *domain.ApplicationMCPEndpoint) { e.Name = "-tickets" },
		"long name":         func(e *domain.ApplicationMCPEndpoint) { e.Name = strings.Repeat("a", 65) },
		"relative URL":      func(e *domain.ApplicationMCPEndpoint) { e.URL = "/mcp" },
		"other scheme":      func(e *domain.ApplicationMCPEndpoint) { e.URL = "ws://desk.example/mcp" },
		"URL credentials":   func(e *domain.ApplicationMCPEndpoint) { e.URL = "https://user:pass@desk.example/mcp" },
		"URL expansion":     func(e *domain.ApplicationMCPEndpoint) { e.URL = "https://${HOST}/mcp" },
		"header name":       func(e *domain.ApplicationMCPEndpoint) { e.Headers["Bad Header"] = deskKey },
		"header line break": func(e *domain.ApplicationMCPEndpoint) { e.Headers["Authorization"] = deskKey + "\r\nX: y" },
		"header expansion":  func(e *domain.ApplicationMCPEndpoint) { e.Headers["Authorization"] = "Bearer ${TALGENT_TOKEN}" },
		"long header value": func(e *domain.ApplicationMCPEndpoint) { e.Headers["Authorization"] = strings.Repeat("k", 8193) },
		"too many headers": func(e *domain.ApplicationMCPEndpoint) {
			for i := range 33 {
				e.Headers["X-"+strings.Repeat("h", i+1)] = "v"
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			endpoint := deskTools()
			change(&endpoint)
			err := endpoint.Validate()
			require.ErrorIs(t, err, domain.ErrInvalidCapabilities)
			require.NotContains(t, err.Error(), deskKey)
		})
	}
}

// A Work's endpoints are few and named apart from each other and from its
// Connectors, with which they share the Harness's server names.
func TestAWorksApplicationEndpointsHaveTheirOwnNames(t *testing.T) {
	connector := connectorcatalogtest.Required().SelectedConnector
	valid := domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connector}, ApplicationEndpoints: []domain.ApplicationMCPEndpoint{deskTools()}}
	require.NoError(t, valid.Validate())

	repeated := domain.CapabilitySnapshot{ApplicationEndpoints: []domain.ApplicationMCPEndpoint{deskTools(), deskTools()}}
	require.ErrorIs(t, repeated.Validate(), domain.ErrInvalidCapabilities)
	shadowing := deskTools()
	shadowing.Name = strings.ToUpper(connector.Name)
	require.ErrorIs(t, (domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connector}, ApplicationEndpoints: []domain.ApplicationMCPEndpoint{shadowing}}).Validate(), domain.ErrInvalidCapabilities)

	many := domain.CapabilitySnapshot{}
	for i := range 17 {
		endpoint := deskTools()
		endpoint.Name = "tools" + strings.Repeat("x", i)
		many.ApplicationEndpoints = append(many.ApplicationEndpoints, endpoint)
	}
	require.ErrorIs(t, many.Validate(), domain.ErrInvalidCapabilities)
	many.ApplicationEndpoints = many.ApplicationEndpoints[:16]
	require.NoError(t, many.Validate())
}

// A Work keeps its own copy of the endpoints it was given: changing the
// caller's value or a clone never changes the Work's headers.
func TestAWorksApplicationEndpointsAreItsOwn(t *testing.T) {
	given := []domain.ApplicationMCPEndpoint{deskTools()}
	snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, ApplicationEndpoints: given}, nil)
	require.NoError(t, err)
	given[0].Headers["Authorization"] = "changed"
	clone := snapshot.Clone()
	clone.ApplicationEndpoints[0].Headers["Authorization"] = "changed"
	require.Equal(t, deskKey, snapshot.ApplicationEndpoints[0].Headers["Authorization"])
}

// Only an Application's Work takes Application MCP endpoints.
func TestOnlyAnApplicationsWorkTakesApplicationEndpoints(t *testing.T) {
	request := func(caller domain.WorkCaller, agentID string) domain.WorkCreationRequest {
		return domain.WorkCreationRequest{StartedBy: caller.ID, WorkCaller: caller, WorkModel: domain.WorkModelSelection{ConfigurationID: "model"},
			Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}, ApplicationEndpoints: []domain.ApplicationMCPEndpoint{deskTools()}},
			Admission:    domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}}
	}
	person, err := domain.NewAgentDefinition(domain.EmployeeDefinitionInput("employee-1"))
	require.NoError(t, err)
	_, _, err = person.CreateWork(request(domain.WorkCaller{Kind: domain.CallerKindPerson, ID: "employee-1"}, person.ID), domain.WorkCreationFacts{})
	require.ErrorIs(t, err, domain.ErrInvalidCapabilities)

	application, err := domain.NewAgentDefinition(domain.ApplicationDefinitionInput(applicationID, "Support Desk"))
	require.NoError(t, err)
	work, created, err := application.CreateWork(request(domain.WorkCaller{Kind: domain.CallerKindApplication, ID: applicationID}, application.ID), domain.WorkCreationFacts{})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, []domain.ApplicationMCPEndpoint{deskTools()}, work.CapabilitySnapshot.ApplicationEndpoints)
}
