package application

import "github.com/talgent-work/workbench/internal/business/trace/application/query"

type Application struct {
	Queries *query.Queries
}

func NewApplication(queries *query.Queries) *Application {
	return &Application{Queries: queries}
}
