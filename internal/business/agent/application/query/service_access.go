package query

// ServiceAccessObservation reports the latest credential-supply observation,
// not proof of authentication or a continuing access grant.
type ServiceAccessObservation struct {
	Service, Target, Plugin, MCP, Status, Reason string
}

type WorkServiceAccess struct {
	OwnerUserID      string
	SelectedServices []string
	Observations     []ServiceAccessObservation
	Repositories     []RepositoryPreparation
}

type RepositoryPreparation struct {
	RepositoryID, TargetPath string
	Prepared                 bool
	Reason                   string
}
