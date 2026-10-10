package bootstrap

import (
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"gooncall-agent/internal/config"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/storage/postgres"
)

// databases 聚合各持久化仓库。
type databases struct {
	incidentRepo repository.Repository
	runRepo      repository.RunRepository
	approvalRepo repository.ApprovalRepository
	// db 只在配置了 PostgreSQL DSN 时非 nil（供就绪检查使用）。
	db *gorm.DB
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
	runRepo := repository.NewPostgresRun(db)
	approvalRepo := repository.NewPostgresApproval(db)
	slog.Info("postgres connected")
	return &databases{
		incidentRepo: incidentRepo,
		runRepo:      runRepo,
		approvalRepo: approvalRepo,
		db:           db,
	}, nil
}
