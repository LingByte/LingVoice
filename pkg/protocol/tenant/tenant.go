// Package tenant provides multi-tenant isolation for the protocol layer.
//
// 多租户架构:
//   - 每个租户 (tenant) 拥有独立的命名空间
//   - 会话按 tenantID 隔离, 不同租户的 session ID 不会冲突
//   - 资源配额: 每个租户可配置最大会话数、带宽限制等
//   - 路由: 协议层请求按 tenantID 路由到对应租户的资源
//
// 使用方式:
//   mgr := tenant.NewManager(tenant.DefaultConfig())
//   mgr.Register("tenant-a", tenant.ResourceQuota{MaxSessions: 100})
//   ctx := tenant.WithContext(context.Background(), "tenant-a")
//   // 后续操作通过 ctx 获取 tenantID
package tenant

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LingByte/ling-base/common/logger"
	"go.uber.org/zap"
)

// contextKey 用于 context 中传递 tenantID
type contextKey struct{}

// ResourceQuota 租户资源配额
type ResourceQuota struct {
	MaxSessions    int   // 最大并发会话数, 0=不限
	MaxBitrate     int64 // 最大带宽 (bps), 0=不限
	MaxRecordings   int   // 最大录制数, 0=不限
	MaxStorageGB   int   // 最大存储空间 (GB), 0=不限
	AllowedProtocols []string // 允许的协议列表, 空=全部允许
}

// DefaultQuota 默认配额 (不限)
func DefaultQuota() ResourceQuota {
	return ResourceQuota{}
}

// Tenant 租户信息
type Tenant struct {
	ID            string
	Name          string
	Quota         ResourceQuota
	activeSessions atomic.Int64
	totalSessions  atomic.Int64
	createdAt      time.Time
}

// Config 多租户管理器配置
type Config struct {
	DefaultQuota ResourceQuota
	// HeaderName 从 HTTP header 获取 tenantID 的字段名
	HeaderName string
	// QueryParam 从 URL query param 获取 tenantID 的参数名
	QueryParam string
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		DefaultQuota: DefaultQuota(),
		HeaderName:   "X-Tenant-ID",
		QueryParam:   "tenant",
	}
}

// Manager 多租户管理器
type Manager struct {
	config   Config
	tenants  sync.Map // map[string]*Tenant
	log      *zap.Logger
}

// NewManager 创建多租户管理器
func NewManager(config Config, log *zap.Logger) *Manager {
	if log == nil {
		if logger.Lg != nil {
			log = logger.Lg
		} else {
			log = zap.NewNop()
		}
	}
	return &Manager{
		config: config,
		log:    log.With(zap.String("component", "tenant-manager")),
	}
}

// Register 注册租户
func (m *Manager) Register(id, name string, quota ResourceQuota) *Tenant {
	t := &Tenant{
		ID:        id,
		Name:      name,
		Quota:     quota,
		createdAt: time.Now(),
	}
	m.tenants.Store(id, t)
	m.log.Info("tenant registered", zap.String("id", id), zap.String("name", name))
	return t
}

// Unregister 注销租户
func (m *Manager) Unregister(id string) {
	m.tenants.Delete(id)
	m.log.Info("tenant unregistered", zap.String("id", id))
}

// Get 获取租户
func (m *Manager) Get(id string) (*Tenant, bool) {
	v, ok := m.tenants.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*Tenant), true
}

// List 列出所有租户
func (m *Manager) List() []*Tenant {
	var list []*Tenant
	m.tenants.Range(func(key, value any) bool {
		list = append(list, value.(*Tenant))
		return true
	})
	return list
}

// AcquireSession 尝试为租户获取一个会话名额
func (m *Manager) AcquireSession(tenantID string) error {
	t, ok := m.Get(tenantID)
	if !ok {
		// 未注册的租户使用默认配额
		t = m.Register(tenantID, tenantID, m.config.DefaultQuota)
	}

	if t.Quota.MaxSessions > 0 {
		current := t.activeSessions.Load()
		if int(current) >= t.Quota.MaxSessions {
			return fmt.Errorf("tenant %s: max sessions (%d) exceeded", tenantID, t.Quota.MaxSessions)
		}
	}

	t.activeSessions.Add(1)
	t.totalSessions.Add(1)
	return nil
}

// ReleaseSession 释放租户的会话名额
func (m *Manager) ReleaseSession(tenantID string) {
	t, ok := m.Get(tenantID)
	if !ok {
		return
	}
	t.activeSessions.Add(-1)
}

// ActiveSessions 返回租户当前活跃会话数
func (m *Manager) ActiveSessions(tenantID string) int64 {
	t, ok := m.Get(tenantID)
	if !ok {
		return 0
	}
	return t.activeSessions.Load()
}

// TotalSessions 返回租户累计会话数
func (m *Manager) TotalSessions(tenantID string) int64 {
	t, ok := m.Get(tenantID)
	if !ok {
		return 0
	}
	return t.totalSessions.Load()
}

// IsProtocolAllowed 检查租户是否允许使用某协议
func (m *Manager) IsProtocolAllowed(tenantID, protocol string) bool {
	t, ok := m.Get(tenantID)
	if !ok {
		return true // 未注册租户允许所有协议
	}
	if len(t.Quota.AllowedProtocols) == 0 {
		return true // 空列表=全部允许
	}
	for _, p := range t.Quota.AllowedProtocols {
		if p == protocol {
			return true
		}
	}
	return false
}

// --- Context 集成 ---

// WithContext 将 tenantID 注入 context
func WithContext(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, contextKey{}, tenantID)
}

// FromContext 从 context 提取 tenantID
func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(contextKey{}).(string); ok {
		return v
	}
	return ""
}
