package cli

import (
	"dosomething/internal/config"
	"dosomething/internal/i18n"
	"dosomething/internal/importp"
	"dosomething/internal/model"
	"dosomething/internal/output"
	"dosomething/internal/store"
	syncer "dosomething/internal/sync"
	"fmt"
	"github.com/alecthomas/kong"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

func nodeSchema(n *kong.Node) map[string]any {
	meta := commandMetadata[n.Name]
	flags := []any{}
	for _, f := range n.Flags {
		entry := map[string]any{"name": f.Name, "type": f.Target.Type().String(), "default": f.Default, "allowed": constraints[f.Tag.Type].Allowed, "constraints": constraints[f.Tag.Type], "aliases": f.Aliases, "repeatable": f.IsCumulative()}
		if description := flagDescription(f.Name); description != "" {
			entry["description"] = description
		}
		if f.IsCumulative() {
			entry["collection"] = map[string]any{"separator": ",", "syntax": "Repeat the flag or provide a comma-separated value; both forms append values."}
		}
		if examples := flagExamples[f.Name]; len(examples) > 0 {
			entry["examples"] = examples
		}
		flags = append(flags, entry)
	}
	args := []any{}
	for i, a := range n.Positional {
		name, description, format := positionalMetadata(n.Name, i, a.Name)
		entry := map[string]any{"name": name, "required": a.Required, "type": a.Target.Type().String(), "allowed": constraints[a.Tag.Type].Allowed, "constraints": constraints[a.Tag.Type], "variadic": a.IsCumulative(), "description": description}
		if format != "" {
			entry["format"] = format
		}
		if examples := positionalExamples[n.Name+"."+name]; len(examples) > 0 {
			entry["examples"] = examples
		}
		args = append(args, entry)
	}
	children := []any{}
	for _, child := range n.Children {
		children = append(children, nodeSchema(child))
	}
	result := map[string]any{"name": n.Name, "flags": flags, "arguments": args, "commands": children}
	if meta.Description != "" {
		result["description"] = meta.Description
	}
	if meta.Response != "" || meta.Output != "" {
		commandOutput := map[string]any{"description": meta.Output}
		if meta.Response != "" {
			commandOutput["response_schema"] = meta.Response
		}
		result["output"] = commandOutput
	}
	if meta.Mutation != "" {
		classification := "conditional"
		if strings.HasPrefix(meta.Mutation, "always") {
			classification = "always"
		}
		if meta.Mutation == "never" || strings.HasPrefix(meta.Mutation, "no logical") || strings.HasPrefix(meta.Mutation, "does not") {
			classification = "never"
		}
		result["state_change"] = map[string]any{"classification": classification, "description": meta.Mutation}
	}
	if len(meta.SideEffects) > 0 {
		result["side_effects"] = meta.SideEffects
	}
	if len(meta.Examples) > 0 {
		result["examples"] = meta.Examples
	}
	return result
}
func schema(k *kong.Kong) any {
	return map[string]any{
		"schema_version": output.SchemaVersion,
		"description":    "Maintain one list of projects and media, then rank what to do or consume next for the user's available time, effort, and preferences.",
		"automation":     map[string]any{"recommended_global_flags": map[string]any{"format": "json", "no-input": true}, "stdout": "Successful JSON responses when --format json is supported; schema is always JSON.", "stderr": "Structured error response when --format json; branch on error.code rather than message.", "references": "Item references accept a full UUID, a unique UUID prefix, or an exact case-sensitive title. Retain full UUIDs in automation."},
		"duration_unit":  "hours",
		"duration_representations": map[string]any{
			"cli_input": "formats.duration", "item_properties": "hours", "activity_duration_fields": "hours",
			"query_filter_fields": map[string]string{"session_seconds": "seconds", "finish_within_seconds": "seconds"},
		},
		"formats":  formatDefinitions(),
		"commands": nodeSchema(k.Model.Node), "default_command": "suggest", "default_invocation": "do-something [suggest flags]",
		"help_topics": helpTopics, "output": jsonShape(reflect.TypeOf(output.Result{})), "responses": responseSchemas(), "interchange": jsonShape(reflect.TypeOf(store.Content{})),
		"kinds": model.AllKinds, "statuses": model.AllStatuses, "modes": model.AllModes, "engagements": model.AllEngagements, "enum_definitions": enumDefinitions(),
		"properties": model.AllProperties(), "property_definitions": propertyDefinitions(),
		"property_states":   []string{"known", "unknown", "not_applicable"},
		"concepts":          conceptDefinitions(),
		"exit_codes":        map[string]string{"0": "success, including zero matches", "1": "runtime failure, unhealthy store, or unresolved sync", "2": "invalid invocation, invalid value or item reference, invalid state, or dependency cycle", "3": "zero matches when --fail-empty is set"},
		"error_codes":       []string{"INVALID_ARGUMENT", "INVALID_ID", "INVALID_STATE", "NO_STORE", "STORE_CORRUPT", "STORE_LOCKED", "SYNC_CONFLICT", "SYNC_IDENTITY_MISMATCH", "NO_PEER_BASE", "SCHEMA_MISMATCH", "CYCLE", "IO_ERROR"},
		"error_definitions": errorDefinitions(),
	}
}

type commandDescription struct {
	Description string
	Response    string
	Output      string
	Mutation    string
	SideEffects []string
	Examples    []string
}

var commandMetadata = map[string]commandDescription{
	"do-something": {Description: "Keep and rank a unified list of projects and media. With no command, runs suggest.", Mutation: "conditional: the default suggest invocation records suggestions unless --peek is set"},
	"suggest":      {Description: "Rank ready, active items for what to do now; defaults to kind=project and records returned suggestions unless --peek is set.", Response: "suggest", Output: "A ranked Result with applied filters, counts, items, readiness, and scores; score_breakdown is populated only with --explain.", Mutation: "conditional: records a suggestion event for returned items unless --peek is set", Examples: []string{"do-something suggest --session 90m --effort medium --peek", "do-something --session 90m --effort medium --peek"}},
	"list":         {Description: "List and optionally rank/filter items; includes every status by default.", Response: "list", Output: "A Result containing matching items and their scores; does not record suggestions.", Mutation: "never", Examples: []string{"do-something list --status in_progress"}},
	"show":         {Description: "Inspect one item, including properties, readiness, dependencies, and score breakdown.", Response: "show", Output: "A Result containing exactly the resolved item with score_breakdown included.", Mutation: "never", Examples: []string{"do-something show ITEM_ID"}},
	"add":          {Description: "Create an item. Non-interactive use requires --title and --kind.", Response: "add", Output: "ItemMutation with changed=true, the new id, before=null, and after containing the item.", Mutation: "always", Examples: []string{"do-something add --title 'Read Dune' --kind media --type book --total-duration 12h --remaining-duration 12h --interest high"}},
	"edit":         {Description: "Update one item; omitted fields are preserved and explicit clear flags make nullable fields unknown.", Response: "edit", Output: "ItemMutation with before and after snapshots and whether anything changed.", Mutation: "conditional: writes only when values change", Examples: []string{"do-something edit ITEM_ID --deadline 2026-12-01", "do-something edit ITEM_ID --clear-deadline --unset-rating interest"}},
	"log":          {Description: "Record one time/cost session and reduce remaining estimates; normally requires the item to be in_progress.", Response: "log", Output: "LogMutation with the event id, actual values, before/after estimates and statuses, and completion result.", Mutation: "always on success", Examples: []string{"do-something log ITEM_ID 90m", "do-something log ITEM_ID --cost 30", "do-something log ITEM_ID 30m --complete"}},
	"start":        {Description: "Set one or more items to in_progress atomically; already-started items are successful no-ops.", Response: "start", Output: "StatusMutation with one before/after transition per item.", Mutation: "conditional: writes status events for changed items", Examples: []string{"do-something start ITEM_ID ANOTHER_ITEM_ID"}},
	"done":         {Description: "Set one or more items to done atomically, unblocking their dependents.", Response: "done", Output: "StatusMutation with one before/after transition per item.", Mutation: "conditional: writes status events for changed items", Examples: []string{"do-something done ITEM_ID"}},
	"drop":         {Description: "Set one or more items to dropped (retired) atomically; dropped dependencies still block dependents.", Response: "drop", Output: "StatusMutation with one before/after transition per item.", Mutation: "conditional: writes status events for changed items", Examples: []string{"do-something drop ITEM_ID"}},
	"reopen":       {Description: "Set one or more items to not_started atomically.", Response: "reopen", Output: "StatusMutation with one before/after transition per item.", Mutation: "conditional: writes changed items", Examples: []string{"do-something reopen ITEM_ID"}},
	"deps":         {Description: "List, add, or remove prerequisite edges. The target item depends on every dependency argument.", Response: "deps", Output: "Dependencies response with before/after dependency ids and resolved item references.", Mutation: "conditional: add/remove write; list never writes", Examples: []string{"do-something deps list TARGET_ITEM", "do-something deps add TARGET_ITEM PREREQUISITE_A PREREQUISITE_B", "do-something deps remove TARGET_ITEM PREREQUISITE_A"}},
	"import":       {Description: "Validate and import a canonical JSON export from a file, or from stdin when source is '-'.", Response: "import", Output: "Import report describing issues and whether data changed.", Mutation: "conditional: writes unless --dry-run is set", Examples: []string{"do-something import export.json --dry-run", "do-something import -"}},
	"export":       {Description: "Write all store content in the canonical interchange format.", Response: "export json", Output: "Canonical Content JSON, not a Result wrapper.", Mutation: "never", Examples: []string{"do-something export json"}},
	"snapshot":     {Description: "Create a validated, consistent single-file SQLite snapshot.", Response: "snapshot", Output: "SnapshotResult containing the written path.", Mutation: "does not change logical store data", SideEffects: []string{"writes a snapshot file"}},
	"sync":         {Description: "Compare and resolve the store against another device snapshot; explicit resolution may be required for conflicts.", Response: "sync", Output: "Sync report with decision, conflicts, archives, baseline, and changed/dry_run flags.", Mutation: "conditional: writes unless --dry-run is set", SideEffects: []string{"may write archive and synchronization-state files"}},
	"config":       {Description: "List effective configuration, get one key, or set/unset a database configuration value.", Response: "config", Output: "ConfigResult with values and their default/database sources.", Mutation: "conditional: set/unset write; list/get never write", Examples: []string{"do-something config", "do-something config set scoring.k.cost_left 500", "do-something config unset scoring.k.cost_left"}},
	"doctor":       {Description: "Validate store integrity, references, configuration, sync state, and diagnostics.", Response: "doctor", Output: "Diagnostic report; unhealthy results also exit 1.", Mutation: "no logical data changes", SideEffects: []string{"may garbage-collect obsolete local history files"}},
	"schema":       {Description: "Print this machine-readable command, concept, output, and error contract.", Output: "This schema document; always JSON regardless of --format.", Mutation: "never"},
	"version":      {Description: "Print application and contract version information.", Response: "version", Output: "VersionResult with --format json; otherwise one text line.", Mutation: "never"},
	"completion":   {Description: "Generate a shell completion script.", Output: "Shell script text for the requested shell.", Mutation: "never"},
	"help":         {Description: "Render command or concept documentation.", Output: "Localized human-readable text; not a structured automation response.", Mutation: "never"},
}

var flagExamples = map[string][]string{
	"session": {"90m", "1.5h"}, "finish-within": {"30m", "2h"}, "deadline": {"2026-12-01"},
	"remaining-duration": {"90m", "1.5h"}, "min-session-duration": {"25m"}, "total-duration": {"12h"},
	"tag":            {"--tag work --tag urgent", "--tag work,urgent"},
	"not-tag":        {"--not-tag waiting --not-tag someday"},
	"depends-on":     {"--depends-on PREREQUISITE_A,PREREQUISITE_B", "--depends-on 'Title with \\, comma'"},
	"modes":          {"--modes movement --modes making", "--modes movement,making"},
	"engagements":    {"--engagements focused,loose"},
	"unset-rating":   {"--unset-rating interest,quality"},
	"clear-deadline": {"--clear-deadline"}, "clear-remaining-duration": {"--clear-remaining-duration"},
	"clear-cost": {"--clear-cost"}, "clear-min-session-duration": {"--clear-min-session-duration"},
	"clear-total-duration": {"--clear-total-duration"}, "clear-tags": {"--clear-tags"},
	"clear-modes": {"--clear-modes"}, "clear-engagements": {"--clear-engagements"}, "clear-dependencies": {"--clear-dependencies"},
}

var positionalExamples = map[string][]string{
	"log.time-passed": {"90m", "1.5h"},
	"start.items":     {"ITEM_ID ANOTHER_ITEM_ID"}, "done.items": {"ITEM_ID ANOTHER_ITEM_ID"},
	"deps.dependencies": {"PREREQUISITE_A PREREQUISITE_B"},
}

func positionalMetadata(command string, index int, fallback string) (string, string, string) {
	item := "Item reference: full UUID, unique UUID prefix, or exact case-sensitive title."
	switch command {
	case "show", "edit", "log":
		if index == 0 {
			return "item", item, "item-reference"
		}
	case "start", "done", "drop", "reopen":
		return "items", "One or more item references; the whole transition is atomic.", "item-reference"
	case "deps":
		switch index {
		case 0:
			return "action", "Dependency operation: list, add, or remove.", ""
		case 1:
			return "target", "The dependent item whose prerequisites are being inspected or changed.", "item-reference"
		case 2:
			return "dependencies", "One or more prerequisite items that the target depends on; required by add/remove and omitted for list.", "item-reference"
		}
	case "import":
		return "source", "Canonical JSON file path, or '-' to read stdin.", "path"
	case "export":
		return "format", "Canonical interchange format to emit.", ""
	case "snapshot":
		return "target", "Destination SQLite file; omitted uses the configured snapshot location.", "path"
	case "sync":
		return "snapshot", "Other device's SQLite snapshot file.", "path"
	case "config":
		switch index {
		case 0:
			return "action", "Optional get, set, or unset operation; omitted lists effective configuration.", ""
		case 1:
			return "key", "Configuration key used by get/set/unset.", ""
		case 2:
			return "value", "String value required by set.", ""
		}
	case "completion":
		return "shell", "Shell whose completion script should be generated.", ""
	case "help":
		return "topic", "Command name or conceptual help topic; omitted shows root help.", ""
	}
	if command == "log" && index == 1 {
		return "time-passed", "Positive active time spent in CLI duration syntax; omit to use the item's known positive min_session_duration.", "duration"
	}
	return fallback, "Command argument.", ""
}

func flagDescription(name string) string {
	if d, ok := map[string]string{
		"db": "Working SQLite database path.", "syncdir": "Directory used for published synchronization snapshots.",
		"format": "Output format for commands that support text/JSON rendering.", "color": "Color policy for text output.", "lang": "Locale for human-readable text; JSON remains canonical English.", "no-input": "Disable interactive prompts; recommended for automation.",
		"effort":        "Maximum activity intensity the user can handle now; keeps known intensity values at or below the selected level.",
		"session":       "Available active time; keeps items whose min_session_duration fits. Alias: --time.",
		"finish-within": "Keep finite items whose remaining_duration is at most this duration; ongoing items never pass.",
		"budget":        "Maximum remaining direct cost in the user's currency-neutral unit.",
		"type":          "Exact case-insensitive match on the user-defined form of an item.", "category": "Exact case-insensitive match on the user-defined topic of an item.",
		"kind":   "Item kind to create, or query scope; suggest defaults to project and both means projects plus media.",
		"status": "Lifecycle state to set or filter.",
		"mode":   "Soft ranking preference for activity kind; never excludes items.", "engagement": "Soft ranking preference for focused versus loose engagement; independent of effort and never excludes items.",
		"not-tag": "Exclude exact case-sensitive tags; repeated values combine with AND.",
		"text":    "Case-insensitive substring filter over title, notes, category, and type.", "min-score": "Minimum computed desirability score from 0 to 1.",
		"limit": "Maximum returned items; 0 means all matches.", "random": "Shuffle matching candidates before applying limit.",
		"peek": "Do not record suggestion events.", "explain": "Include score_breakdown details in item output.",
		"include-unready": "Include items blocked by unfinished prerequisites; suggest hides them by default.",
		"unknown":         "Policy for filtered properties: include lets unknown values pass with filter_evidence; exclude requires known matching values.",
		"fail-empty":      "Exit 3 instead of 0 when a query has zero matches.",
		"ongoing":         "Whether the activity has no finish line; ongoing items cannot have total_duration or remaining_duration.",
		"title":           "Item title; exact titles can later be used as references.", "notes": "Free-text notes.", "url": "Associated URL.",
		"deadline": "Last calendar day on which the item can be completed.", "remaining-duration": "Estimated active time still needed to finish.",
		"cost": "On add/edit, remaining direct cost; on log, positive cost spent in this session.", "min-session-duration": "Smallest worthwhile active session.", "total-duration": "Estimated total active time from start to finish.",
		"tag":        "On add/edit, tags to add; on queries, required exact case-sensitive tags. Repeat or comma-separate values.",
		"depends-on": "Replace the item's complete prerequisite set with these item references; repeat or comma-separate values.",
		"modes":      "Built-in activity modes suited to the item; repeat or comma-separate values.", "engagements": "Built-in engagement styles suited to the item; repeat or comma-separate values.",
		"unset-rating":   "Make the named rating properties unknown; repeat or comma-separate property names.",
		"clear-deadline": "Set deadline to unknown/null.", "clear-remaining-duration": "Set remaining_duration to unknown/null.", "clear-cost": "Set cost_left to unknown/null.",
		"clear-min-session-duration": "Set min_session_duration to unknown/null.", "clear-total-duration": "Set total_duration to unknown/null.",
		"clear-tags": "Replace tags with an empty set.", "clear-modes": "Replace modes with an empty, unclassified set.", "clear-engagements": "Replace engagements with an empty, unclassified set.", "clear-dependencies": "Replace prerequisites with an empty set.",
		"complete": "After logging time, mark the item done only if remaining_duration reaches zero.", "no-complete": "Suppress interactive completion without marking done.", "force": "Allow logging outside in_progress; does not bypass missing estimates or validation.",
		"dry-run": "Validate and report without applying changes.", "keep-local": "Resolve a sync conflict by keeping local content.", "take-remote": "Resolve/bootstrap by taking remote snapshot content.", "merge": "Perform a three-way merge.", "master": "For merge conflicts, prefer local or remote values.",
	}[name]; ok {
		return d
	}
	if d, ok := propertyDescriptions[name]; ok {
		return d
	}
	return ""
}

var propertyDescriptions = map[string]string{
	"intensity": "Intrinsic demand rating: 0=no energy/attention demand; 1=most intense. Used by --effort, not desirability scoring.",
	"career":    "Project professional-development value: 0=no benefit; 1=excellent benefit.",
	"physical":  "Project physical-health value: 0=no benefit; 1=excellent benefit.",
	"mental":    "Project mental-health/cognitive value: 0=no benefit; 1=excellent benefit.",
	"social":    "Project relationship/community value: 0=no benefit; 1=excellent benefit.",
	"interest":  "Personal appeal for either kind: 0=no interest; 1=can't wait.",
	"actuality": "Media timeliness in current culture/debate: 0=stale; 1=timely.",
	"influence": "Media cultural/genre influence: 0=trivial; 1=culture-defining reference.",
	"quality":   "Media quality: 0=poor; 1=acclaimed. Unknown is null, not zero.",
}

func formatDefinitions() map[string]any {
	return map[string]any{
		"duration": map[string]any{
			"description": "Go duration syntax: bare 0, or one or more decimal number+unit components; units are ns, us/µs, ms, s, m, and h. Nonzero values require a unit; days are not supported. Exposed duration constraints reject negative values. Item properties and activity JSON use hours; echoed query filter fields named *_seconds use seconds.",
			"grammar":     "0 | +?(([0-9]+(\\.[0-9]+)?|\\.[0-9]+)(ns|us|µs|ms|s|m|h))+ (no spaces)",
			"examples":    map[string]any{"accepted": []string{"90m", "1.5h", "1h30m", "30s"}, "rejected": []string{"1.5", "90", "1d"}},
		},
		"date":           map[string]any{"description": "Calendar date with no time or timezone.", "grammar": "YYYY-MM-DD (a real Gregorian date)", "examples": []string{"2026-12-01"}},
		"rating":         map[string]any{"description": "A finite number from 0 through 1, or a canonical level name mapped to its anchor value.", "examples": []any{0, 0.5, 1, "low", "medium", "max"}},
		"item-reference": map[string]any{"description": "Full UUID, unique UUID prefix, or exact case-sensitive title. Ambiguous and missing references fail with INVALID_ID; automation should retain full UUIDs."},
		"path":           map[string]any{"description": "Filesystem path interpreted by the named command; '-' is accepted only where that positional explicitly documents stdin."},
	}
}

func enumDefinitions() map[string]any {
	return map[string]any{
		"kind":              map[string]string{"project": "Something done for benefit or necessity.", "media": "Something consumed for relaxation, fun, or interest.", "both": "Query-only union of project and media."},
		"status":            map[string]string{"not_started": "Available but not started.", "in_progress": "Started and not finished.", "done": "Finished; only this status satisfies a dependency.", "dropped": "Retired, not completed; still blocks dependents."},
		"mode":              map[string]string{"movement": "Move the body.", "hands_on": "Build, craft, tinker, fix, or maintain with the hands.", "thinking": "Study, analyze, read, or solve using the mind.", "making": "Bring an unfinished artifact or work into existence."},
		"engagement":        map[string]string{"focused": "Sustained, relatively undivided attention.", "loose": "Relaxed, intermittent, low-key engagement; not a low-effort rating."},
		"effort":            map[string]any{"description": "Maximum acceptable intensity, ordered from least to most demanding.", "anchors": map[string]float64{"none": 0, "low": 1.0 / 3.0, "medium": 0.5, "high": 2.0 / 3.0, "very_high": 0.8, "max": 1}},
		"unknown_policy":    map[string]string{"include": "Unknown filtered values pass and are identified in filter_evidence.", "exclude": "A filtered value must be known and satisfy the filter."},
		"property_state":    map[string]string{"known": "A value is present.", "unknown": "Applicable but no value is known; never interpret as numeric zero.", "not_applicable": "The property does not apply to this item kind or state."},
		"dependency_action": map[string]string{"list": "Inspect prerequisites without mutation.", "add": "Add prerequisite edges.", "remove": "Remove prerequisite edges."},
	}
}

func propertyDefinitions() map[string]any {
	definitions := map[string]any{}
	applicable := map[string][]string{
		"intensity": {"project", "media"}, "career": {"project"}, "physical": {"project"}, "mental": {"project"}, "social": {"project"},
		"interest": {"project", "media"}, "actuality": {"media"}, "influence": {"media"}, "quality": {"media"},
		"remaining_duration": {"project", "media"}, "cost_left": {"project", "media"}, "min_session_duration": {"project", "media"}, "total_duration": {"project", "media"}, "deadline": {"project", "media"},
	}
	for name, kinds := range applicable {
		d := map[string]any{"applicable_kinds": kinds}
		if description := propertyDescriptions[name]; description != "" {
			d["description"] = description
		}
		if model.Property(name).IsRating() {
			d["type"] = "rating"
			d["range"] = []float64{0, 1}
		}
		definitions[name] = d
	}
	definitions["remaining_duration"] = map[string]any{"type": "duration_hours", "applicable_kinds": applicable["remaining_duration"], "description": "Estimated active hours still needed to finish; less remaining time raises commitment desirability and --finish-within filters it. Unknown is null."}
	definitions["cost_left"] = map[string]any{"type": "nonnegative_number", "applicable_kinds": applicable["cost_left"], "description": "Remaining direct cost in the user's currency-neutral unit; lower is less commitment. Unknown is null."}
	definitions["min_session_duration"] = map[string]any{"type": "duration_hours", "applicable_kinds": applicable["min_session_duration"], "description": "Smallest worthwhile active session; --session keeps items at or below available time. Unknown is null."}
	definitions["total_duration"] = map[string]any{"type": "duration_hours", "applicable_kinds": applicable["total_duration"], "description": "Estimated total active hours; used for progress context, not scoring. Unknown is null."}
	definitions["deadline"] = map[string]any{"type": "date", "applicable_kinds": applicable["deadline"], "description": "Last day the item can still be completed; contributes urgency. Unknown is null.", "examples": []string{"2026-12-01"}}
	return definitions
}

func conceptDefinitions() map[string]any {
	return map[string]any{
		"effort":                  "A query-time ceiling on the item's intrinsic intensity rating: --effort medium admits known intensity <= the configurable medium anchor. It is not engagement style or a score input.",
		"mode":                    "What kind of activity a session involves. Items may have several modes; a query mode soft-reranks fit=1, unclassified=0.5, mismatch=0 and never filters.",
		"engagement":              "How attention is applied (focused or loose), independent of mode and effort. Query engagement soft-reranks with fit=1, unclassified=0.5, mismatch=0 and never filters.",
		"readiness":               "An item is ready only when every prerequisite dependency is done. suggest hides unready items unless --include-unready; list/show report readiness.blocked_by.",
		"dependencies":            "Directed prerequisite edges: target depends on prerequisite. Non-done prerequisites block target; done unblocks it, dropped does not. Cycles are rejected atomically with CYCLE.",
		"unknown_property_policy": "Unknown means applicable but unset, never numeric zero. Query filters include unknowns by default and report filter_evidence; --unknown exclude requires known values. Scoring normally uses the configurable 0.5 prior.",
		"nullable_fields":         map[string]any{"description": "Edit preserves omitted values. Clear measurement/date fields with their --clear-* flags; clear ratings with --unset-rating; clear collections with --clear-tags/--clear-modes/--clear-engagements/--clear-dependencies.", "examples": []string{"do-something edit ITEM_ID --clear-deadline --clear-cost", "do-something edit ITEM_ID --unset-rating interest,quality", "do-something edit ITEM_ID --clear-dependencies"}},
		"scoring":                 "Scores are bounded 0..1. Base desirability is a weighted normalized blend of value, commitment, and interest groups; unknown properties use the prior. Right-now terms cover urgency, momentum, unblocking, cooldown, and requested mode/engagement matches. Default final blend is 80% base and 20% right-now; --explain returns score_breakdown.",
		"ongoing":                 "An activity with no finish line. total_duration and remaining_duration are not_applicable; logs record sessions without a countdown; --finish-within excludes it.",
	}
}

func errorDefinitions() map[string]any {
	return map[string]any{
		"transport": "With --format json, failures are one ErrorResult JSON object on stderr and no success response on stdout.",
		"shape":     "error always has code, message, value, and details; INVALID_ARGUMENT may also carry argument, allowed, bounds, and format.",
		"codes": map[string]string{
			"INVALID_ARGUMENT": "Invocation syntax or a parameter value is invalid; exit 2.", "INVALID_ID": "Item reference is missing or ambiguous; exit 2.",
			"INVALID_STATE": "Operation is invalid for the current item state; exit 2.", "CYCLE": "Dependency change would create a cycle; exit 2.",
			"NO_STORE": "No database exists at the selected path; exit 1.", "STORE_CORRUPT": "Store integrity or content validation failed; exit 1.",
			"STORE_LOCKED": "A writer holds the store lock; exit 1.", "SYNC_CONFLICT": "Sync needs an explicit resolution; exit 1.",
			"SYNC_IDENTITY_MISMATCH": "Snapshot belongs to a different database identity; exit 1.", "NO_PEER_BASE": "Three-way sync has no peer baseline; exit 1.",
			"SCHEMA_MISMATCH": "Store/interchange schema is unsupported; exit 1.", "IO_ERROR": "Filesystem or stream operation failed; exit 1.",
		},
	}
}

// help renders the root page for node == nil / the model root, and a
// per-command page for a command node. All prose is localized; flag names,
// value lists, and format hints are canonical.
func help(k *kong.Kong, node *kong.Node, tr *i18n.Translator, w io.Writer) {
	if node == nil || node == k.Model.Node {
		helpRoot(k, tr, w)
		return
	}
	helpCommand(k, node, tr, w)
}

// helpTopics are the concept pages behind `do-something help <topic>`: the
// data-model concepts, one page per rating and measurement property, the
// relationship and query concepts, and the mechanics.
var helpTopics = []string{
	"kind", "type", "category",
	"intensity", "career", "physical", "mental", "social", "interest",
	"actuality", "influence", "quality",
	"remaining_duration", "cost_left", "min_session_duration", "total_duration", "deadline",
	"dependencies", "status", "effort", "ongoing",
	"modes", "engagement", "properties", "scoring", "filters",
}

// helpTopic renders the root page for an empty topic, a command page for a
// command name, a concept page for a topic, and an error otherwise.
func helpTopic(k *kong.Kong, topic string, tr *i18n.Translator, w io.Writer) error {
	if topic == "" {
		helpRoot(k, tr, w)
		return nil
	}
	for _, child := range k.Model.Children {
		if child.Name == topic {
			helpCommand(k, child, tr, w)
			return nil
		}
	}
	for _, t := range helpTopics {
		if topic == t {
			fmt.Fprintln(w, tr.T("help.topic."+t))
			return nil
		}
	}
	allowed := append([]string{}, helpTopics...)
	for _, child := range k.Model.Children {
		allowed = append(allowed, child.Name)
	}
	sort.Strings(allowed)
	return invalid(&argumentError{"topic", topic, constraint{Allowed: allowed}, fmt.Errorf("allowed: %s", strings.Join(allowed, ", "))})
}
func helpRoot(k *kong.Kong, tr *i18n.Translator, w io.Writer) {
	fmt.Fprintln(w, tr.T("help.usage"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, tr.T("help.root"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, tr.T("help.commands"))
	summaries := []string{}
	width := 0
	for _, n := range k.Model.Children {
		summary := usageSummary(n)
		summaries = append(summaries, summary)
		if len(summary) > width {
			width = len(summary)
		}
	}
	for i, n := range k.Model.Children {
		fmt.Fprintf(w, "  %-*s  %s\n", width, summaries[i], tr.T("help."+n.Name))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, tr.T("help.default_command"))
	fmt.Fprintln(w, tr.T("help.default_kind"))
	fmt.Fprintln(w, tr.T("help.references"))
	fmt.Fprintln(w)
	fmt.Fprintln(w, tr.T("help.topics"))
	fmt.Fprintln(w, "  "+strings.Join(helpTopics, ", "))
	fmt.Fprintln(w)
	if len(k.Model.Flags) > 0 {
		fmt.Fprintln(w, tr.T("help.flags"))
		for _, f := range k.Model.Flags {
			fmt.Fprintln(w, "  "+flagUsage(f, tr))
		}
		fmt.Fprintln(w)
	}
	helpValues(tr, w)
}
func helpCommand(k *kong.Kong, n *kong.Node, tr *i18n.Translator, w io.Writer) {
	fmt.Fprintln(w, tr.T("help.usage_command", "command", usageSummary(n)))
	fmt.Fprintln(w)
	fmt.Fprintln(w, tr.T("help."+n.Name))
	if n.Name == "show" || n.Name == "edit" || n.Name == "log" || n.Name == "start" || n.Name == "done" || n.Name == "drop" || n.Name == "reopen" || n.Name == "deps" || n.Name == "add" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, tr.T("help.references"))
	}
	if len(n.Positional) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, tr.T("help.positionals"))
		for _, p := range n.Positional {
			hint := positionalHint(p)
			if hint == "" {
				hint = tr.T("help.arg." + p.Name)
			}
			fmt.Fprintf(w, "  %-12s %s\n", positionalText(p), hint)
		}
	}
	if len(n.Flags) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, tr.T("help.flags"))
		for _, f := range n.Flags {
			fmt.Fprintln(w, "  "+flagUsage(f, tr))
		}
	}
	if n.Name == "suggest" || n.Name == "list" {
		keys := []string{"help.filter.and", "help.filter.type", "help.filter.text", "help.filter.mode", "help.filter.engagement", "help.filter.unknown"}
		if n.Name == "suggest" {
			keys = append(keys, "help.filter.kind")
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, tr.T("help.notes"))
		for _, key := range keys {
			fmt.Fprintln(w, "  "+tr.T(key))
		}
	}
	if len(k.Model.Flags) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, tr.T("help.global_flags"))
		for _, f := range k.Model.Flags {
			fmt.Fprintln(w, "  "+flagUsage(f, tr))
		}
	}
}

// usageSummary renders the command line of a node: "name <positional…> [flags]".
func usageSummary(n *kong.Node) string {
	s := n.Name
	for _, p := range n.Positional {
		s += " " + positionalText(p)
	}
	if len(n.Flags) > 0 {
		s += " [flags]"
	}
	return s
}
func positionalText(p *kong.Positional) string {
	name := p.Name
	if name == "id" {
		name = "id|title"
	}
	if p.IsCumulative() {
		name += "..."
	}
	if p.Required {
		return "<" + name + ">"
	}
	return "[" + name + "]"
}

// positionalHint lists the accepted values of a constrained positional; empty
// for free-form positionals (the caller then shows a localized description).
func positionalHint(p *kong.Positional) string {
	if allowed := constraints[p.Tag.Type].Allowed; len(allowed) > 0 {
		return strings.Join(allowed, " | ")
	}
	return ""
}

// flagUsage renders one flag line: the name plus, for value flags, the hint
// from the shared constraint table, then default and alias annotations.
func flagUsage(f *kong.Flag, tr *i18n.Translator) string {
	line := "--" + f.Name
	if !f.IsBool() {
		line += "=" + flagHint(f)
	}
	if f.HasDefault && f.Default != "" {
		line += " " + tr.T("help.default", "value", f.Default)
	}
	if len(f.Aliases) > 0 {
		line += " " + tr.T("help.alias", "name", "--"+strings.Join(f.Aliases, ", --"))
	}
	return line
}

// flagHint is the value hint of a flag: allowed values, a format, or a range.
// It falls back to Kong's placeholder for unconstrained flags.
func flagHint(f *kong.Flag) string {
	if f.Name == "depends-on" {
		return "ID|TITLE,..."
	}
	c := constraints[f.Tag.Type]
	if len(c.Allowed) > 0 {
		return strings.Join(c.Allowed, "|")
	}
	switch c.Format {
	case "duration":
		return "DURATION"
	case "date":
		return "YYYY-MM-DD"
	case "rating":
		return "LEVEL|0..1"
	}
	if c.Minimum != nil && c.Maximum != nil {
		return fmt.Sprintf("%g..%g", *c.Minimum, *c.Maximum)
	}
	if c.Minimum != nil {
		return fmt.Sprintf(">= %g", *c.Minimum)
	}
	if c.Maximum != nil {
		return fmt.Sprintf("<= %g", *c.Maximum)
	}
	return f.FormatPlaceHolder()
}

// helpValues documents the constrained value sets (from the shared constraint
// table), the kind-scoped rating/measurement properties, and the exit codes.
func helpValues(tr *i18n.Translator, w io.Writer) {
	fmt.Fprintln(w, tr.T("help.values"))
	order := []string{"kind", "kind-filter", "status", "mode", "engagement", "effort", "unknown", "output-format", "data-format", "color", "lang", "master", "deps-action", "config-action", "shell", "rating-property", "rating", "unit", "nonnegative", "duration", "date"}
	render := func(key string) {
		c := constraints[key]
		hint := strings.Join(c.Allowed, ", ")
		if hint == "" {
			switch c.Format {
			case "duration":
				hint = tr.T("help.format_duration")
			case "date":
				hint = "YYYY-MM-DD"
			case "rating":
				hint = "LEVEL|0..1"
			}
		}
		if hint == "" {
			if c.Minimum != nil && c.Maximum != nil {
				hint = fmt.Sprintf("%g..%g", *c.Minimum, *c.Maximum)
			} else if c.Minimum != nil {
				hint = fmt.Sprintf(">= %g", *c.Minimum)
			} else if c.Maximum != nil {
				hint = fmt.Sprintf("<= %g", *c.Maximum)
			}
		}
		fmt.Fprintf(w, "  %-17s %s\n", key, hint)
	}
	seen := map[string]bool{}
	for _, key := range order {
		if _, ok := constraints[key]; !ok {
			continue
		}
		seen[key] = true
		render(key)
	}
	extra := []string{}
	for key := range constraints {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		render(key)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, tr.T("help.ratings"))
	for _, kind := range model.AllKinds {
		names := []string{}
		for _, p := range model.RatingProperties(kind) {
			names = append(names, string(p))
		}
		if len(names) == 0 {
			fmt.Fprintf(w, "  %-8s %s\n", kind, tr.T("help.none"))
		} else {
			fmt.Fprintf(w, "  %-8s %s\n", kind, strings.Join(names, ", "))
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, tr.T("help.measurements"))
	for _, kind := range model.AllKinds {
		names := []string{}
		for _, p := range model.MeasurementProperties(kind) {
			names = append(names, string(p))
		}
		fmt.Fprintf(w, "  %-8s %s\n", kind, strings.Join(names, ", "))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, tr.T("help.exit_codes"))
	for _, code := range []string{"0", "1", "2", "3"} {
		fmt.Fprintf(w, "  %s  %s\n", code, tr.T("help.exit."+code))
	}
}
func completion(k *kong.Kong, shell string, w io.Writer) {
	commands := []string{}
	flags := map[string]*kong.Flag{}
	addFlags := func(n *kong.Node) {
		for _, f := range n.Flags {
			flags[f.Name] = f
			for _, alias := range f.Aliases {
				flags[alias] = f
			}
		}
	}
	addFlags(k.Model.Node)
	for _, n := range k.Model.Children {
		commands = append(commands, n.Name)
		addFlags(n)
	}
	names := []string{}
	for name := range flags {
		names = append(names, name)
	}
	sort.Strings(names)
	words := append([]string{}, commands...)
	for _, name := range names {
		words = append(words, "--"+name)
	}
	switch shell {
	case "bash":
		fmt.Fprintln(w, "_do_something() {\n  local cur=\"${COMP_WORDS[COMP_CWORD]}\" prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n  case \"$prev\" in")
		for _, name := range names {
			if allowed := constraints[flags[name].Tag.Type].Allowed; len(allowed) > 0 {
				fmt.Fprintf(w, "    --%s) COMPREPLY=( $(compgen -W '%s' -- \"$cur\") ); return ;;\n", name, strings.Join(allowed, " "))
			}
		}
		fmt.Fprintf(w, "  esac\n  COMPREPLY=( $(compgen -W '%s' -- \"$cur\") )\n}\ncomplete -F _do_something do-something\n", strings.Join(words, " "))
	case "zsh":
		fmt.Fprintln(w, "#compdef do-something\n_arguments -s \\")
		for _, name := range names {
			suffix := ""
			f := flags[name]
			if !f.IsBool() {
				suffix = ":value:"
				if allowed := constraints[f.Tag.Type].Allowed; len(allowed) > 0 {
					suffix += "(" + strings.Join(allowed, " ") + ")"
				}
			}
			fmt.Fprintf(w, "  '--%s%s' \\\n", name, suffix)
		}
		fmt.Fprintf(w, "  '*:command:(%s)'\n", strings.Join(commands, " "))
	case "fish":
		for _, name := range names {
			suffix := ""
			f := flags[name]
			if !f.IsBool() {
				suffix = " -r"
			}
			if allowed := constraints[f.Tag.Type].Allowed; len(allowed) > 0 {
				suffix += " -a '" + strings.Join(allowed, " ") + "'"
			}
			fmt.Fprintf(w, "complete -c do-something -l %s%s\n", name, suffix)
		}
		fmt.Fprintf(w, "complete -c do-something -n '__fish_use_subcommand' -a '%s'\n", strings.Join(commands, " "))
	}
}
func doctor(s *store.Store, d *config.DeviceConfig, tr *i18n.Translator) (any, error) {
	problems := []string{}
	checks := s.Diagnostics()
	for _, name := range []string{"openability", "quick_check", "foreign_key_check", "schema"} {
		if checks[name] != "ok" {
			problems = append(problems, name+": "+checks[name])
		}
	}
	content, e := s.Content()
	if e != nil {
		return nil, e
	}
	if e = content.Validate(); e != nil {
		problems = append(problems, e.Error())
	}
	cfg, e := s.ConfigMap()
	if e != nil {
		return nil, e
	}
	for k, v := range cfg {
		if e = config.ValidateValue(k, v); e != nil {
			problems = append(problems, e.Error())
		}
	}
	id, e := s.DatabaseUUID()
	if e != nil {
		return nil, e
	}
	held, holder, since := s.LockInfo()
	snapshots := []snapshotReport{}
	if d.SyncDir != "" {
		entries, e := os.ReadDir(filepath.Join(d.SyncDir, "snapshots"))
		if e != nil && !os.IsNotExist(e) {
			problems = append(problems, e.Error())
		}
		for _, entry := range entries {
			path := filepath.Join(d.SyncDir, "snapshots", entry.Name())
			if strings.Contains(entry.Name(), "sync-conflict-") {
				problems = append(problems, path)
			}
			if filepath.Ext(path) != ".db" {
				continue
			}
			c, identity, e := store.ReadSnapshot(path)
			if e != nil {
				problems = append(problems, path+": "+e.Error())
				continue
			}
			digest, _ := c.Digest(true)
			peer := strings.TrimSuffix(entry.Name(), ".db")
			baseline, _, e := s.PeerBaseline(peer)
			if e != nil {
				problems = append(problems, e.Error())
			}
			snapshots = append(snapshots, snapshotReport{path, identity, digest, baseline != digest})
		}
	}
	usage := int64(0)
	_ = filepath.WalkDir(s.StateDir(), func(path string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() {
			if info, e := d.Info(); e == nil {
				usage += info.Size()
			}
		}
		return nil
	})
	blocked := []blockedReport{}
	items, e := s.AllItems()
	if e != nil {
		return nil, e
	}
	for _, it := range items {
		b, e := s.BlockedBy(it.ID)
		if e != nil {
			return nil, e
		}
		if len(b) > 0 {
			blocked = append(blocked, blockedReport{it.ID, b})
		}
	}
	gc, e := s.HistoryGC()
	if e != nil {
		problems = append(problems, e.Error())
	}
	afterUsage := directoryBytes(s.StateDir())
	effective := config.NewConfig(cfg)
	effectiveValues := map[string]output.ConfigValue{}
	for _, key := range effective.Keys() {
		effectiveValues[key] = output.ConfigValue{Value: effective.StringValue(key), Source: effective.Source(key)}
	}
	completeness, e := i18n.CatalogCompleteness()
	if e != nil {
		problems = append(problems, e.Error())
	}
	rep := doctorReport{Checks: checks, SchemaVersion: output.SchemaVersion, Healthy: len(problems) == 0, Problems: problems, StorePath: s.Path(), DatabaseUUID: id, DatabaseSchemaVersion: store.SchemaVersion, Lock: lockReport{held, holder, since}, DeviceConfig: d, DBConfig: cfg, EffectiveConfig: effectiveValues, Snapshots: snapshots, Blocked: blocked, GCRemoved: gc, StateBytes: afterUsage, ReclaimedBytes: max(int64(0), usage-afterUsage), SyncStateBytes: directoryBytes(s.SyncStateDir()), HistoryBytes: directoryBytes(s.HistoryDir()), CatalogCompleteness: completeness, Locale: tr.Locale(), Locales: i18n.KnownLocales}
	if len(problems) > 0 {
		return rep, fmt.Errorf("doctor found %d problems", len(problems))
	}
	return rep, nil
}

func jsonShape(t reflect.Type) any {
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if t == reflect.TypeOf(store.Content{}) {
		properties := map[string]any{"schema_version": map[string]any{"type": "integer", "const": store.SchemaVersion}}
		required := []string{"schema_version"}
		for _, table := range store.EmptyContent().Tables() {
			fields := map[string]any{}
			for _, column := range table.Columns {
				fields[column] = map[string]any{}
				if table.Name == "items" && column == "ongoing" {
					fields[column] = map[string]any{"type": "integer", "enum": []int{0, 1}}
				}
				if isDurationProperty(column) {
					fields[column] = durationShape()
				}
			}
			properties[table.Name] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": fields, "required": table.Columns}}
			required = append(required, table.Name)
		}
		return map[string]any{"type": "object", "properties": properties, "required": required}
	}
	for typ, values := range map[reflect.Type]any{reflect.TypeOf(model.Kind("")): model.AllKinds, reflect.TypeOf(model.Status("")): model.AllStatuses, reflect.TypeOf(model.PropertyState("")): []string{"known", "unknown", "not_applicable"}} {
		if t == typ {
			return map[string]any{"type": "string", "enum": values}
		}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return map[string]any{"anyOf": []any{jsonShape(t.Elem()), map[string]any{"type": "null"}}}
	case reflect.Struct:
		p := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			key := strings.Split(f.Tag.Get("json"), ",")[0]
			if key == "-" {
				continue
			}
			if key == "" {
				key = f.Name
			}
			p[key] = jsonShape(f.Type)
			if isDurationProperty(key) {
				p[key] = durationShape()
			}
			if key == "estimated_progress_percent" {
				p[key] = map[string]any{"type": []string{"number", "null"}, "minimum": 0, "maximum": 100}
			}
			if !strings.Contains(f.Tag.Get("json"), "omitempty") {
				required = append(required, key)
			}
		}
		if _, ok := p["schema_version"]; ok {
			p["schema_version"] = map[string]any{"type": "integer", "const": output.SchemaVersion}
		}
		if t == reflect.TypeOf(model.LoggedActivity{}) {
			for _, key := range []string{"duration", "remaining_before", "remaining_after"} {
				p[key] = map[string]any{"type": []string{"number", "null"}, "minimum": 0, "description": "Active duration in hours; null for ongoing activity."}
			}
			p["duration"] = map[string]any{"type": "number", "exclusiveMinimum": 0, "description": "Full time spent in hours, including any overrun."}
		}
		if t == reflect.TypeOf(output.Item{}) {
			values, states := map[string]any{}, map[string]any{}
			keys := []string{}
			for _, property := range model.AllProperties() {
				name := string(property)
				keys = append(keys, name)
				shape := map[string]any{"type": []string{"number", "null"}, "minimum": 0}
				if property.IsRating() {
					shape["maximum"] = 1
				}
				if property == model.PropDeadline {
					shape = map[string]any{"type": []string{"string", "null"}, "format": "date"}
				}
				if isDurationProperty(name) {
					shape = durationShape()
				}
				values[name] = shape
				states[name] = jsonShape(reflect.TypeOf(model.PropertyState("")))
			}
			p["properties"] = map[string]any{"type": "object", "properties": values, "required": keys}
			p["property_states"] = map[string]any{"type": "object", "properties": states, "required": keys}
		}
		return map[string]any{"type": "object", "properties": p, "required": required}
	case reflect.Slice:
		return map[string]any{"type": []string{"array", "null"}, "items": jsonShape(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": []string{"object", "null"}, "additionalProperties": jsonShape(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{}
	}
}
func advisory(s *store.Store, d *config.DeviceConfig, tr *i18n.Translator, w io.Writer) {
	if d.SyncDir == "" {
		return
	}
	entries, e := os.ReadDir(filepath.Join(d.SyncDir, "snapshots"))
	if e != nil {
		return
	}
	own, _ := os.ReadFile(filepath.Join(s.StateDir(), "device-id"))
	for _, entry := range entries {
		path := filepath.Join(d.SyncDir, "snapshots", entry.Name())
		peer := strings.TrimSuffix(entry.Name(), ".db")
		if peer == strings.TrimSpace(string(own)) {
			continue
		}
		if strings.Contains(entry.Name(), "sync-conflict-") {
			fmt.Fprintln(w, tr.T("sync.advisory", "path", path))
			continue
		}
		if filepath.Ext(path) != ".db" {
			continue
		}
		c, _, e := store.ReadSnapshot(path)
		if e != nil {
			fmt.Fprintln(w, tr.T("sync.invalid_snapshot", "path", path))
			continue
		}
		digest, _ := c.Digest(true)
		base, _, e := s.PeerBaseline(peer)
		if e == nil && base != digest {
			fmt.Fprintln(w, tr.T("sync.advisory", "path", path))
		}
	}
}

func directoryBytes(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry os.DirEntry, e error) error {
		if e == nil && !entry.IsDir() {
			if info, e := entry.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func responseSchemas() map[string]any {
	responses := map[string]any{}
	for name, value := range map[string]any{
		"suggest": output.Result{}, "list": output.Result{}, "show": output.Result{},
		"log": output.LogMutation{}, "add": output.ItemMutation{}, "edit": output.ItemMutation{},
		"start": output.StatusMutation{}, "done": output.StatusMutation{}, "drop": output.StatusMutation{}, "reopen": output.StatusMutation{},
		"deps": output.Dependencies{}, "config": output.ConfigResult{}, "import": importp.Report{}, "sync": syncer.Report{},
		"snapshot": output.SnapshotResult{}, "version": output.VersionResult{}, "doctor": doctorReport{}, "export json": store.Content{}, "error": output.ErrorResult{},
	} {
		shape := jsonShape(reflect.TypeOf(value)).(map[string]any)
		shape["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		responses[name] = shape
	}
	return responses
}

func isDurationProperty(name string) bool {
	return name == string(model.PropTotalDuration) || name == string(model.PropRemainingDuration) || name == string(model.PropMinSessionDuration)
}
func durationShape() map[string]any {
	return map[string]any{"type": []string{"number", "null"}, "minimum": 0, "description": "Active duration in hours."}
}
