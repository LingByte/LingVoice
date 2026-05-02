package listeners

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/LingByte/LingVoice/internal/models"
	"github.com/LingByte/LingVoice/pkg/notification/sms"
	"gorm.io/gorm"
)

type smsEnv struct {
	Provider string         `json:"provider"`
	Config   map[string]any `json:"config"`
}

// EnabledSMSChannels returns enabled SMS channels for an org, ordered by SortOrder.
func EnabledSMSChannels(db *gorm.DB, orgID uint) ([]sms.SenderChannel, error) {
	if db == nil {
		return nil, errors.New("nil db")
	}
	var rows []models.NotificationChannel
	if err := db.Where("org_id = ? AND type = ? AND enabled = ?", orgID, models.NotificationChannelTypeSMS, true).
		Order("sort_order ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]sms.SenderChannel, 0, len(rows))
	for _, row := range rows {
		raw := strings.TrimSpace(row.ConfigJSON)
		if raw == "" {
			continue
		}
		var env smsEnv
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			continue
		}
		kind := sms.ProviderKind(strings.ToLower(strings.TrimSpace(env.Provider)))
		if kind == "" {
			continue
		}
		p, err := sms.NewProviderFromKindMap(kind, env.Config)
		if err != nil {
			continue
		}
		out = append(out, sms.SenderChannel{Label: row.Name, Provider: p})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no enabled sms channels for org %d", orgID)
	}
	return out, nil
}

