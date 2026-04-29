package dbutils

import (
	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"gorm.io/gorm"
)

// GetDBTimestamp returns a UNIX timestamp from database time.
// 分布式系统必须用数据库统一时间
// 如果你的服务部署在多台服务器每台服务器时间可能差几秒～几分钟会导致：
// 订单时间错乱, 登录日志时间不对, 数据统计异常
// Falls back to application time on error.
func GetDBTimestamp(db *gorm.DB, driver string) int64 {
	var ts int64
	var err error
	switch driver {
	case constants.DatabaseTypePostgreSQL:
		err = db.Raw("SELECT EXTRACT(EPOCH FROM NOW())::bigint").Scan(&ts).Error
	case constants.DatabaseTypeSQLite:
		err = db.Raw("SELECT strftime('%s','now')").Scan(&ts).Error
	default:
		err = db.Raw("SELECT UNIX_TIMESTAMP()").Scan(&ts).Error
	}
	if err != nil || ts <= 0 {
		return base.GetTimestamp()
	}
	return ts
}
