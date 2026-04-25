// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package listeners

import (
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// InitApplicationListeners registers all process-wide signal listeners (system, user, async pools).
// Call once after configuration and database are ready.
func InitApplicationListeners(db *gorm.DB, lg *zap.Logger) {
	InitSystemListeners()
	InitUserSignalListeners(db, lg)
}
