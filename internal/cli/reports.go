package cli

import (
	"dosomething/internal/config"
	"dosomething/internal/output"
	"dosomething/internal/store"
)

type lockReport struct {
	Held  bool   `json:"held"`
	PID   int    `json:"pid"`
	Since string `json:"since"`
}
type snapshotReport struct {
	Path                 string `json:"path"`
	DatabaseUUID         string `json:"database_uuid"`
	Digest               string `json:"digest"`
	ChangedSinceObserved bool   `json:"changed_since_observed"`
}
type blockedReport struct {
	ID        string          `json:"id"`
	BlockedBy []store.Blocker `json:"blocked_by"`
}
type doctorReport struct {
	Checks                  map[string]string             `json:"checks"`
	SchemaVersion           int                           `json:"schema_version"`
	Healthy                 bool                          `json:"healthy"`
	Problems                []string                      `json:"problems"`
	StorePath               string                        `json:"store_path"`
	DatabaseUUID            string                        `json:"database_uuid"`
	DatabaseSchemaVersion   int                           `json:"database_schema_version"`
	Lock                    lockReport                    `json:"lock"`
	DeviceConfig            *config.DeviceConfig          `json:"device_config"`
	DBConfig                map[string]string             `json:"db_config"`
	EffectiveConfig         map[string]output.ConfigValue `json:"effective_config"`
	Snapshots               []snapshotReport              `json:"snapshots"`
	Blocked                 []blockedReport               `json:"blocked"`
	GCRemoved               []string                      `json:"gc_removed"`
	StateBytes              int64                         `json:"state_bytes"`
	ReclaimedBytes          int64                         `json:"reclaimed_bytes"`
	ReclaimableBytesAfterGC int64                         `json:"reclaimable_bytes_after_gc"`
	SyncStateBytes          int64                         `json:"sync_state_bytes"`
	HistoryBytes            int64                         `json:"history_bytes"`
	CatalogCompleteness     map[string][]string           `json:"catalog_completeness"`
	Locale                  string                        `json:"locale"`
	Locales                 []string                      `json:"locales"`
}
