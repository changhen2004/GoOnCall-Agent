// knowledgeimport 是独立的知识导入命令，不向模型开放导入能力。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"gooncall-agent/cmd/internal/knowledgecfg"
	"gooncall-agent/configs"
	"gooncall-agent/internal/knowledge/rag"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径（默认 AGENT_CONFIG 或 configs/config.env）")
	flag.Parse()

	if err := run(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := cfg.ValidateRAG(); err != nil {
		return err
	}

	builder, err := rag.New(knowledgecfg.From(cfg))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	count, err := builder.Import(ctx)
	if err != nil {
		return fmt.Errorf("导入知识库: %w", err)
	}
	fmt.Printf("知识导入完成：%d 个 Runbook 片段已索引到 %s/%s\n",
		count, cfg.Qdrant.URL, cfg.Qdrant.Collection)
	return nil
}
