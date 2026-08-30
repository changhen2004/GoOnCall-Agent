package bootstrap

import (
	"fmt"
	"log/slog"

	"gooncall-agent/internal/config"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/storage/postgres"
)

// databases 聚合各持久化仓库。
type databases struct {
	incidentRepo repository.Repository
	runRepo      repository.RunRepository
	approvalRepo repository.ApprovalRepository
}

// buildDatabases 构建各仓库（无 DSN 时回退内存，有 DSN 时共享连接）。
func buildDatabases(cfg *config.Config) (*databases, error) {
	if cfg.Postgres.DSN == "" {
		slog.Warn("POSTGRES_DSN is empty, falling back to in-memory repositories")
		return &databases{
			incidentRepo: repository.NewMemory(),
			runRepo:      repository.NewMemoryRun(),
			approvalRepo: repository.NewMemoryApproval(),
		}, nil
	}

	db, err := postgres.Connect(cfg.Postgres.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	incidentRepo := repository.NewPostgres(db)
	if err := incidentRepo.AutoMigrate(); err != nil {
		return nil, fmt.Errorf("auto migrate incident: %w", err)
	}
	runRepo := repository.NewPostgresRun(db)
	if err := runRepo.AutoMigrate(); err != nil {
		return nil, fmt.Errorf("auto migrate run: %w", err)
	}
	approvalRepo := repository.NewPostgresApproval(db)
	if err := approvalRepo.AutoMigrate(); err != nil {
		return nil, fmt.Errorf("auto migrate approval: %w", err)
	}
	slog.Info("postgres connected and migrated")
	return &databases{incidentRepo: incidentRepo, runRepo: runRepo, approvalRepo: approvalRepo}, nil
}
