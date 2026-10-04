package cli

// This single definition drives both Kong parsing and the schema command.
type QueryFlags struct {
	Effort         string   `type:"effort"`
	Session        string   `aliases:"time" type:"duration"`
	FinishWithin   string   `type:"duration"`
	Budget         *float64 `type:"nonnegative"`
	Type           string   `placeholder:"TYPE"`
	Category       string   `placeholder:"CATEGORY"`
	Kind           string   `type:"kind-filter"`
	Status         string   `type:"status"`
	Mode           string   `placeholder:"MODE" type:"mode"`
	Engagement     string   `placeholder:"ENGAGEMENT" type:"engagement"`
	Tag            []string
	NotTag         []string
	Text           string  `placeholder:"TEXT"`
	MinScore       float64 `type:"unit"`
	Limit          *int    `type:"nonnegative"`
	Random         bool
	Peek           bool
	Explain        bool
	IncludeUnready bool
	Unknown        string `type:"unknown"`
	FailEmpty      bool
}
type Entry struct {
	Ongoing                 *bool  `help:""`
	Title                   string `placeholder:"TITLE"`
	Kind                    string `type:"kind"`
	Status                  string `type:"status"`
	Type                    *string
	Category                *string
	Notes                   *string
	URL                     *string  `name:"url"`
	Deadline                *string  `type:"date"`
	RemainingDuration       *float64 `type:"duration"`
	Cost                    *float64 `type:"nonnegative"`
	MinSessionDuration      *float64 `type:"duration"`
	TotalDuration           *float64 `type:"duration"`
	Intensity               *float64 `type:"rating"`
	Career                  *float64 `type:"rating"`
	Physical                *float64 `type:"rating"`
	Mental                  *float64 `type:"rating"`
	Social                  *float64 `type:"rating"`
	Interest                *float64 `type:"rating"`
	Actuality               *float64 `type:"rating"`
	Influence               *float64 `type:"rating"`
	Quality                 *float64 `type:"rating"`
	Tag                     []string
	DependsOn               []string
	Modes                   []string `type:"mode"`
	Engagements             []string `type:"engagement"`
	UnsetRating             []string `type:"rating-property"`
	ClearDeadline           bool
	ClearRemainingDuration  bool
	ClearCost               bool
	ClearMinSessionDuration bool
	ClearTotalDuration      bool
	ClearTags               bool
	ClearModes              bool
	ClearEngagements        bool
	ClearDependencies       bool
}
type LogFlags struct {
	ID         string   `arg:""`
	Duration   *float64 `arg:"" optional:"" name:"time-passed" type:"positive-duration"`
	Cost       *float64 `type:"positive"`
	Complete   bool     `xor:"completion"`
	NoComplete bool     `xor:"completion"`
	Force      bool
}
type IDs struct {
	IDs []string `arg:"" required:"" name:"id"`
}
type Args struct {
	DB      string `placeholder:"DB"`
	Syncdir string `placeholder:"SYNCDIR"`
	Format  string `default:"text" type:"output-format"`
	Color   string `type:"color"`
	Lang    string `type:"lang"`
	NoInput bool
	Suggest QueryFlags `cmd:"" default:"withargs"`
	List    QueryFlags `cmd:""`
	Show    struct {
		ID string `arg:""`
	} `cmd:""`
	Add  Entry `cmd:""`
	Edit struct {
		ID string `arg:""`
		Entry
	} `cmd:""`
	Log    LogFlags `cmd:""`
	Start  IDs      `cmd:""`
	Done   IDs      `cmd:""`
	Drop   IDs      `cmd:""`
	Reopen IDs      `cmd:""`
	Deps   struct {
		Action string   `arg:"" type:"deps-action"`
		ID     string   `arg:""`
		IDs    []string `arg:"" optional:"" name:"id"`
	} `cmd:""`
	Import struct {
		File   string `arg:""`
		DryRun bool
	} `cmd:""`
	Export struct {
		Format string `arg:"" type:"data-format"`
	} `cmd:""`
	Snapshot struct {
		Target string `arg:"" optional:""`
	} `cmd:""`
	Sync struct {
		File       string `arg:""`
		DryRun     bool
		KeepLocal  bool
		TakeRemote bool
		Merge      bool
		Master     string `type:"master"`
	} `cmd:""`
	Config struct {
		Action string  `arg:"" optional:"" type:"config-action"`
		Key    string  `arg:"" optional:""`
		Value  *string `arg:"" optional:""`
	} `cmd:""`
	Doctor     struct{} `cmd:""`
	Schema     struct{} `cmd:""`
	Version    struct{} `cmd:""`
	Completion struct {
		Shell string `arg:"" type:"shell"`
	} `cmd:""`
	Help struct {
		Topic string `arg:"" optional:"" name:"topic"`
	} `cmd:""`
}
