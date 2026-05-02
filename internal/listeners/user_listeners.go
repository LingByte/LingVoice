// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package listeners

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/LingByte/LingVoice"
	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/constants"
	"github.com/LingByte/LingVoice/pkg/logger"
	"github.com/LingByte/LingVoice/pkg/notification/mail"
	"github.com/LingByte/LingVoice/pkg/utils/base"
	"github.com/LingByte/LingVoice/pkg/utils/task"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	userSigOnce sync.Once

	emailVerifyMailPool = task.NewTaskPool[emailVerifyCodeJob, struct{}](&task.PoolOption{
		WorkerCount: 4,
		QueueSize:   512,
		Logger:      zap.L(),
	})
)

type emailVerifyCodeJob struct {
	db        *gorm.DB
	userID    uint
	email     string
	code      string
	clientIP  string
	userAgent string
}

// InitUserSignalListeners wires user-related signals: synchronous logging and async side effects (e.g. mail).
func InitUserSignalListeners(db *gorm.DB, lg *zap.Logger) {
	if lg == nil {
		lg = zap.NewNop()
	}
	if db == nil {
		lg.Warn("InitUserSignalListeners: nil db; verify-email mail jobs require *gorm.DB in signal params")
	}

	userSigOnce.Do(func() {
		registerUserSignalHandlers(lg)
	})
}

func registerUserSignalHandlers(lg *zap.Logger) {
	base.Sig().Connect(constants.SigUserLogin, func(sender any, params ...any) {
		u, _ := sender.(*models.User)
		lg.Info("signal user.login",
			zap.String("email", emailOf(u)),
			zap.Int("extraParams", len(params)),
		)
	})

	base.Sig().Connect(constants.SigUserLogout, func(sender any, params ...any) {
		u, _ := sender.(*models.User)
		lg.Info("signal user.logout",
			zap.String("email", emailOf(u)),
		)
	})

	base.Sig().Connect(constants.SigUserCreate, func(sender any, params ...any) {
		u, _ := sender.(*models.User)
		lg.Info("signal user.create",
			zap.String("email", emailOf(u)),
		)
	})

	base.Sig().Connect(constants.SigUserVerifyEmail, func(sender any, params ...any) {
		u, ok := sender.(*models.User)
		if !ok || u == nil || len(params) < 4 {
			lg.Warn("signal user.verifyemail: bad payload")
			return
		}
		code, _ := params[0].(string)
		clientIP, _ := params[1].(string)
		userAgent, _ := params[2].(string)
		db, ok := params[3].(*gorm.DB)
		if !ok || db == nil {
			lg.Warn("signal user.verifyemail: missing db")
			return
		}
		lg.Info("signal user.verifyemail",
			zap.Uint("userId", u.ID),
			zap.String("email", u.Email),
			zap.String("clientIp", clientIP),
			zap.Int("uaLen", len(userAgent)),
		)
		job := emailVerifyCodeJob{
			db:        db,
			userID:    u.ID,
			email:     u.Email,
			code:      code,
			clientIP:  clientIP,
			userAgent: userAgent,
		}
		_, err := emailVerifyMailPool.AddTask(context.Background(), job, sendEmailLoginCodeHTML)
		if err != nil {
			lg.Warn("verify email mail async queue", zap.Error(err))
		}
	})
}

func emailOf(u *models.User) string {
	if u == nil {
		return ""
	}
	return u.Email
}

func sendEmailLoginCodeHTML(ctx context.Context, job emailVerifyCodeJob) (struct{}, error) {
	var out struct{}
	if job.db == nil || strings.TrimSpace(job.email) == "" || strings.TrimSpace(job.code) == "" {
		return out, fmt.Errorf("invalid email login code mail job")
	}
	cfgs, err := EnabledMailConfigs(job.db)
	if err != nil {
		logger.Warn("email login code mail: no mail channels", zap.Error(err))
		return out, err
	}
	orgID := uint(0)
	var u models.User
	if err := job.db.Where("id = ?", job.userID).First(&u).Error; err == nil {
		_ = models.EnsurePersonalOrg(job.db, &u)
		orgID = u.DefaultOrgID
	}
	mailer, err := mail.NewMailer(cfgs, job.db, job.clientIP, mail.WithMailLogUserID(job.userID), mail.WithMailLogOrgID(orgID))
	if err != nil {
		logger.Warn("email login code mail: mailer", zap.Error(err))
		return out, err
	}
	type codeTpl struct {
		Username   string
		Code       string
		ExpireHint string
	}
	html, err := LingVoice.RenderHTML(LingVoice.TplEmailLoginCode, codeTpl{
		Username:   LingVoice.UsernameFromEmail(job.email),
		Code:       job.code,
		ExpireHint: "10 分钟",
	})
	if err != nil {
		logger.Error("email login code mail: template render failed", zap.Error(err), zap.String("to", job.email))
		return out, err
	}
	subject := "登录验证码"
	if err := mailer.SendHTML(ctx, job.email, subject, html); err != nil {
		logger.Error("email login code mail: send failed", zap.Error(err), zap.String("to", job.email))
		return out, err
	}
	return out, nil
}
