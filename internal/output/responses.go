package output

import (
	"dosomething/internal/model"
	"dosomething/internal/store"
)

// These response types are shared by emitters and machine-readable schema discovery.
type ErrorResult struct {
	SchemaVersion int       `json:"schema_version"`
	Error         ErrorBody `json:"error"`
}
type ErrorBody struct {
	ExclusiveMinimum *float64 `json:"exclusiveMinimum,omitempty"`
	Code             string   `json:"code"`
	Message          string   `json:"message"`
	Argument         string   `json:"argument,omitempty"`
	Value            any      `json:"value"`
	Allowed          []string `json:"allowed,omitempty"`
	Minimum          *float64 `json:"minimum,omitempty"`
	Maximum          *float64 `json:"maximum,omitempty"`
	Format           string   `json:"format,omitempty"`
	Details          any      `json:"details"`
}
type ItemMutation struct {
	SchemaVersion int         `json:"schema_version"`
	Changed       bool        `json:"changed"`
	ID            string      `json:"id"`
	Before        *model.Item `json:"before"`
	After         *model.Item `json:"after"`
}
type StatusMutation struct {
	SchemaVersion int                      `json:"schema_version"`
	Changed       bool                     `json:"changed"`
	Items         []store.TransitionResult `json:"items"`
	AllIDs        []string                 `json:"-"`
}
type ItemReference struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
type Dependencies struct {
	SchemaVersion   int             `json:"schema_version"`
	Changed         bool            `json:"changed"`
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	Before          []string        `json:"before"`
	After           []string        `json:"after"`
	Dependencies    []string        `json:"dependencies"`
	BeforeItems     []ItemReference `json:"before_items"`
	AfterItems      []ItemReference `json:"after_items"`
	DependencyItems []ItemReference `json:"dependency_items"`
	AllIDs          []string        `json:"-"`
}
type ConfigValue struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}
type ConfigResult struct {
	SchemaVersion int                    `json:"schema_version"`
	Changed       bool                   `json:"changed"`
	Config        map[string]ConfigValue `json:"config"`
}
type SnapshotResult struct {
	SchemaVersion int    `json:"schema_version"`
	Path          string `json:"path"`
}
type VersionResult struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
}

// LogMutation records one session; repeated invocations are distinct sessions.
type LogMutation struct {
	SchemaVersion int             `json:"schema_version"`
	Changed       bool            `json:"changed"`
	Log           store.LogResult `json:"log"`
}
