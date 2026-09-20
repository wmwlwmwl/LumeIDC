package plugin

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
)

// Info 插件元信息。Name 为唯一标识：小写蛇形，与插件目录同名。
type Info struct {
	Name        string
	Title       string
	Version     string
	Description string
}

// Plugin 业务类插件接口。实现后在自己的 init() 中 Register 即完成接入。
type Plugin interface {
	Info() Info
	// Init 插件初始化：保存 Host、订阅事件。Init 失败将导致启动失败。
	Init(h *Host) error
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Plugin{}
)

// Register 注册插件，由插件包 init() 调用。重名/缺名 panic：编译期冲突应在启动早期暴露。
func Register(p Plugin) {
	name := p.Info().Name
	if name == "" {
		panic("plugin: 插件缺少 Name")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("plugin: 插件名重复注册: %s", name))
	}
	registry[name] = p
}

// All 返回全部已注册插件（含禁用），按 Name 排序（启动顺序稳定）。
func All() []Plugin {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Plugin, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Info().Name < out[j].Info().Name })
	return out
}

// ---- 运行时启停（软禁用：即时生效，无需重启） ----
// 禁用 = 事件不投递、路由 404、菜单/清单不显示、cron 不触发；插件迁移不受影响。
// 状态存 plugins 表（无记录 = 默认启用），启动时读入内存，切换时同步更新。

var (
	enabledMu   sync.RWMutex
	enabledDB   *sql.DB
	enabledMemo = map[string]bool{} // 仅记录显式设置过的插件；缺省 = 启用
)

// InitEnabledStore 启动时加载启用态（组合根在核心迁移完成后调用）。
// 表尚不存在（老库升级前）时静默降级为全部启用——下次启动迁移完成后生效。
func InitEnabledStore(db *sql.DB) error {
	enabledMu.Lock()
	enabledDB = db
	enabledMemo = map[string]bool{}
	enabledMu.Unlock()
	rows, err := db.QueryContext(context.Background(), `SELECT name,enabled FROM plugins`)
	if err != nil {
		return nil // 表不存在等场景静默：全部启用
	}
	defer rows.Close()
	memo := map[string]bool{}
	for rows.Next() {
		var name string
		var enabled bool
		if err := rows.Scan(&name, &enabled); err != nil {
			continue
		}
		memo[name] = enabled
	}
	enabledMu.Lock()
	enabledMemo = memo
	enabledMu.Unlock()
	return rows.Err()
}

// Enabled 查询插件是否启用（缺省启用）。
func Enabled(name string) bool {
	enabledMu.RLock()
	defer enabledMu.RUnlock()
	if en, ok := enabledMemo[name]; ok {
		return en
	}
	return true
}

// SetEnabled 切换启用态：写表（幂等 upsert）+ 更新内存。
func SetEnabled(ctx context.Context, name string, enabled bool) error {
	enabledMu.RLock()
	db := enabledDB
	enabledMu.RUnlock()
	if db == nil {
		return fmt.Errorf("插件启用存储未初始化")
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO plugins(name,enabled,updated_at) VALUES($1,$2,now())
		 ON CONFLICT(name) DO UPDATE SET enabled=EXCLUDED.enabled, updated_at=now()`,
		name, enabled); err != nil {
		return fmt.Errorf("更新插件启用状态失败: %w", err)
	}
	enabledMu.Lock()
	enabledMemo[name] = enabled
	enabledMu.Unlock()
	return nil
}
