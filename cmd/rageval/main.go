package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gooncall-agent/internal/bootstrap"
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/knowledge/evaluation"
)

func main() {
	configPath := flag.String("config", "configs/config.local.yaml", "配置文件路径")
	qrelsPath := flag.String("qrels", "docs/eval/runbook_qrels.jsonl", "标注查询 JSONL")
	flag.Parse()

	if err := run(*configPath, *qrelsPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(configPath, qrelsPath string) error {
	qrels, err := loadQrels(qrelsPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	retriever, err := bootstrap.BuildRunbookRetriever(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	runs := make(map[string][]string, len(qrels))
	for _, qrel := range qrels {
		results, err := retriever.Retrieve(ctx, qrel.Query, 8)
		if err != nil {
			return fmt.Errorf("retrieve %s: %w", qrel.ID, err)
		}
		for _, result := range results {
			runs[qrel.ID] = append(runs[qrel.ID], result.Chunk.DocID)
		}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(evaluation.Evaluate(qrels, runs, 3, 5, 8))
}

func loadQrels(path string) ([]evaluation.Qrel, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open qrels: %w", err)
	}
	defer file.Close()

	seen := make(map[string]struct{})
	var qrels []evaluation.Qrel
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		var qrel evaluation.Qrel
		if err := json.Unmarshal(scanner.Bytes(), &qrel); err != nil {
			return nil, fmt.Errorf("decode qrels line %d: %w", line, err)
		}
		if strings.TrimSpace(qrel.ID) == "" || strings.TrimSpace(qrel.Query) == "" ||
			strings.TrimSpace(qrel.Component) == "" || len(qrel.RelevantDocIDs) == 0 {
			return nil, fmt.Errorf("qrels line %d has empty required fields", line)
		}
		if !qrel.Reviewed {
			return nil, fmt.Errorf("qrels line %d (%s) requires human review", line, qrel.ID)
		}
		if _, ok := seen[qrel.ID]; ok {
			return nil, fmt.Errorf("duplicate query_id %q", qrel.ID)
		}
		seen[qrel.ID] = struct{}{}
		qrels = append(qrels, qrel)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read qrels: %w", err)
	}
	if len(qrels) == 0 {
		return nil, fmt.Errorf("qrels is empty")
	}
	return qrels, nil
}
