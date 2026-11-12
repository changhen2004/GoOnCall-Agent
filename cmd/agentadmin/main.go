// agentadmin 是本地账号管理命令。
//
// 它只操作 SQLite，不取得执行进程锁、不处理遗留运行，因此可以与运行中的
// Agent 服务同时使用。密码从标准输入读取，不接受命令行明文参数。
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/term"

	"gooncall-agent/configs"
	"gooncall-agent/internal/auth"
	"gooncall-agent/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	configPath := "configs/config.env"
	if len(args) >= 2 && args[0] == "-config" {
		configPath = args[1]
		args = args[2:]
	}
	if len(args) == 0 {
		return usage()
	}
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	username := fs.String("username", "", "用户名")
	userRef := fs.String("user", "", "用户 ID 或用户名")
	legacyActor := fs.String("legacy-actor", "", "旧固定 Token 操作者标识")
	userID := fs.String("user-id", "", "目标新用户 ID")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.AgentService.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch command {
	case "create-user":
		return createUser(ctx, db, *username)
	case "disable-user":
		return setUserEnabled(ctx, db, *userRef, false)
	case "enable-user":
		return setUserEnabled(ctx, db, *userRef, true)
	case "reset-password":
		return resetPassword(ctx, db, *userRef)
	case "migrate-owner":
		return migrateOwner(ctx, db, *legacyActor, *userID)
	default:
		return usage()
	}
}

func usage() error {
	return errors.New(`用法:
  agentadmin [-config configs/config.env] create-user --username <name>
  agentadmin [-config ...] disable-user --user <id|username>
  agentadmin [-config ...] enable-user --user <id|username>
  agentadmin [-config ...] reset-password --user <id|username>
  agentadmin [-config ...] migrate-owner --legacy-actor <old> --user-id <uuid>

密码从标准输入读取；不要在命令行传入明文密码。`)
}

func createUser(ctx context.Context, db *store.DB, username string) error {
	username = strings.TrimSpace(username)
	if !validUsername(username) {
		return errors.New("用户名需为 1-64 位字母、数字、点、下划线或连字符")
	}
	password, err := readPassword("设置密码: ", true)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now()
	user := auth.User{
		ID: uuid.NewString(), Username: username, PasswordHash: hash,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateUser(ctx, user); err != nil {
		return err
	}
	fmt.Printf("已创建用户 %s（id=%s）\n", user.Username, user.ID)
	return nil
}

func setUserEnabled(ctx context.Context, db *store.DB, ref string, enabled bool) error {
	user, err := resolveUser(ctx, db, ref)
	if err != nil {
		return err
	}
	now := time.Now()
	var changed bool
	if enabled {
		changed, err = db.EnableUser(ctx, user.ID, now)
	} else {
		changed, err = db.DisableUser(ctx, user.ID, now)
	}
	if err != nil {
		return err
	}
	if !changed {
		fmt.Printf("用户 %s 已处于目标状态\n", user.Username)
		return nil
	}
	if enabled {
		fmt.Printf("已启用用户 %s；旧登录 Token 不会恢复\n", user.Username)
	} else {
		fmt.Printf("已禁用用户 %s 并撤销全部 Token；运行中服务将在检测周期内取消其活动运行\n", user.Username)
	}
	return nil
}

func resetPassword(ctx context.Context, db *store.DB, ref string) error {
	user, err := resolveUser(ctx, db, ref)
	if err != nil {
		return err
	}
	password, err := readPassword("设置新密码: ", true)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	changed, err := db.ResetUserPassword(ctx, user.ID, hash, time.Now())
	if err != nil {
		return err
	}
	if !changed {
		return auth.ErrNotFound
	}
	fmt.Printf("已重置用户 %s 的密码并撤销全部 Token\n", user.Username)
	return nil
}

func migrateOwner(ctx context.Context, db *store.DB, legacyActor, userID string) error {
	legacyActor = strings.TrimSpace(legacyActor)
	userID = strings.TrimSpace(userID)
	if legacyActor == "" || userID == "" {
		return errors.New("必须同时提供 --legacy-actor 和 --user-id")
	}
	count, err := db.MigrateLegacyOwner(ctx, legacyActor, userID, time.Now())
	if err != nil {
		return err
	}
	if count == 0 {
		fmt.Printf("没有可迁移的旧会话（legacy actor=%s）\n", legacyActor)
		return nil
	}
	fmt.Printf("已迁移 %d 个旧会话到用户 %s；审计记录已写入 ownership_migrations\n", count, userID)
	return nil
}

func resolveUser(ctx context.Context, db *store.DB, ref string) (*auth.User, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("必须提供 --user")
	}
	if user, err := db.UserByID(ctx, ref); err == nil {
		return user, nil
	}
	user, err := db.UserByUsername(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("用户 %s 不存在", ref)
	}
	return user, nil
}

func validUsername(username string) bool {
	if len(username) == 0 || len(username) > 64 {
		return false
	}
	for _, r := range username {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// readPassword 优先使用终端无回显读取；管道输入时读取一行。
func readPassword(prompt string, confirm bool) (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, prompt)
		first, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if confirm {
			fmt.Fprint(os.Stderr, "再次输入: ")
			second, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return "", err
			}
			if string(first) != string(second) {
				return "", errors.New("两次输入的密码不一致")
			}
		}
		password := string(first)
		if len(password) < 8 || len(password) > 128 {
			return "", errors.New("密码长度需为 8-128 个字符")
		}
		return password, nil
	}

	reader := bufio.NewReader(io.LimitReader(os.Stdin, 4096))
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	password := strings.TrimRight(line, "\r\n")
	if len(password) < 8 || len(password) > 128 {
		return "", errors.New("密码长度需为 8-128 个字符")
	}
	return password, nil
}
