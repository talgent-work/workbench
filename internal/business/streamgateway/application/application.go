package application

import "github.com/talgent-work/workbench/internal/business/streamgateway/application/query"

type Queries struct {
	WatchStream           *query.WatchStream
	ListMessagesBySession *query.ListMessagesBySessionHandler
}

type Application struct {
	Queries Queries
	reader  query.UnifiedMessageReader
	access  query.WorkAccessPort
}

func NewApplication(watch *query.WatchStream, list *query.ListMessagesBySessionHandler, reader query.UnifiedMessageReader, access query.WorkAccessPort) *Application {
	return &Application{Queries: Queries{WatchStream: watch, ListMessagesBySession: list}, reader: reader, access: access}
}
