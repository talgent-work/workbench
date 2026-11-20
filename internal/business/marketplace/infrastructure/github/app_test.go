package github

import "testing"

// A GitHub Enterprise Server's token never reaches GitHub.com's services; a
// GitHub.com App's reaches any host its Provider declares.
func TestAnEnterpriseServerAppKeepsItsTokensOffGitHubDotCom(t *testing.T) {
	enterprise := Config{WebBaseURL: "https://github.example.com", APIBaseURL: "https://github.example.com/api/v3"}
	for _, pattern := range []string{"api.github.com", "*.github.com", "github.com", "api.githubcopilot.com", "*.githubcopilot.com"} {
		if enterprise.MaySendCredentialTo(pattern) || !(Config{}).MaySendCredentialTo(pattern) {
			t.Fatalf("%s: only a GitHub.com App's token reaches it", pattern)
		}
	}
	if !enterprise.MaySendCredentialTo("github.example.com") || !enterprise.MaySendCredentialTo("notgithub.com") {
		t.Fatal("an Enterprise Server's token reaches the hosts its Provider declares")
	}
}
