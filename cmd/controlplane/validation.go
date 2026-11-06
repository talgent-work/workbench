package main

import (
	"fmt"
)

func validateOption(opt Option) error {
	for _, component := range []struct {
		name     string
		validate func() error
	}{
		{name: "storage", validate: opt.Storage.Validate},
		{name: "marketplace-taskqueue", validate: opt.MarketplaceTaskQueue.Validate},
		{name: "agent-taskqueue", validate: opt.AgentTaskQueue.Validate},
		{name: "agent-preparation-taskqueue", validate: opt.AgentPreparationTaskQueue.Validate},
		{name: "storage-preview-taskqueue", validate: opt.StoragePreviewTaskQueue.Validate},
		{name: "marketplace", validate: opt.Marketplace.Validate},
		{name: "agent-marketplace", validate: opt.AgentMarketplace.Validate},
		{name: "account", validate: opt.Account.Validate},
		{name: "agent-user-directory", validate: opt.AgentUserDirectory.Validate},
		{name: "trace-clickhouse", validate: opt.TraceClickHouse.Validate},
		{name: "trace-access", validate: opt.TraceAccess.Validate},
	} {
		if err := component.validate(); err != nil {
			return fmt.Errorf("%s config: %w", component.name, err)
		}
	}
	return nil
}
