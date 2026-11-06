package query

type ProviderConnectionState struct {
	AuthorizationID             string
	Provider                    string
	Status                      string
	ProviderAccountID           string
	ProviderLogin               string
	CanListRepositories         bool
	RecommendedAction           string
	ReasonCode                  string
	ReasonMessage               string
	AccessTokenExpiresAtUnixMS  int64
	RefreshTokenExpiresAtUnixMS int64
	LastCheckedAtUnixMS         int64
	LastFailureCode             string
}
