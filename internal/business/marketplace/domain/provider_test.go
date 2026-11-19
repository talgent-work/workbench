package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func TestProviderTarget_ContainsOnlyItsHosts(t *testing.T) {
	target := domain.ProviderTarget{Name: "internal", Hosts: []domain.HostPattern{"mcp.example.com", "*.svc.example.com"}}
	for address, inside := range map[string]bool{
		"https://mcp.example.com/mcp":           true,
		"https://MCP.example.com:443/mcp":       true,
		"https://docs.svc.example.com/mcp":      true,
		"https://a.b.svc.example.com/":          true,
		"https://svc.example.com/mcp":           false,
		"https://mcp.example.com.evil.test/mcp": false,
		"http://mcp.example.com/mcp":            false,
		"https://mcp.example.com:8443/mcp":      false,
		"https://user@mcp.example.com/mcp":      false,
		"https://other.example.com/mcp":         false,
	} {
		require.Equal(t, inside, target.Contains(address), address)
	}
}

func TestProviderDeclaration_Validates(t *testing.T) {
	valid := domain.ProviderDeclaration{Name: "example-sso", DisplayName: "Example", Derivation: domain.SharedTargetCredential,
		Targets: []domain.ProviderTarget{{Name: "docs", Hosts: []domain.HostPattern{"docs.example.com"}}}}
	require.NoError(t, valid.Validate())
	for name, change := range map[string]func(*domain.ProviderDeclaration){
		"no targets":         func(d *domain.ProviderDeclaration) { d.Targets = nil },
		"bare wildcard":      func(d *domain.ProviderDeclaration) { d.Targets[0].Hosts = []domain.HostPattern{"*"} },
		"top-level wildcard": func(d *domain.ProviderDeclaration) { d.Targets[0].Hosts = []domain.HostPattern{"*.com"} },
		"unknown derivation": func(d *domain.ProviderDeclaration) { d.Derivation = "token-exchange" },
	} {
		declaration := valid
		declaration.Targets = []domain.ProviderTarget{{Name: "docs", Hosts: []domain.HostPattern{"docs.example.com"}}}
		change(&declaration)
		require.ErrorIs(t, declaration.Validate(), domain.ErrInvalidProviderDeclaration, name)
	}
}
