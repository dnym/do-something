// Package cli wires the command contract to the application services.
package cli

import (
	"bufio"
	"dosomething/internal/config"
	"dosomething/internal/i18n"
	"dosomething/internal/importp"
	"dosomething/internal/model"
	"dosomething/internal/output"
	"dosomething/internal/paths"
	"dosomething/internal/store"
	syncer "dosomething/internal/sync"
	"errors"
	"fmt"
	"github.com/alecthomas/kong"
	"github.com/mattn/go-isatty"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

var Version = "0.1.0-dev"

type invocationError struct{ error }

func (e invocationError) Unwrap() error { return e.error }

func invalid(e error) error { return invocationError{e} }
func flag(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}
func Run(argv []string, in *os.File, out, errout io.Writer) int {
	pin := ""
	if local, err := config.ReadLocalFile(paths.LocalConfigFile()); err == nil {
		pin = local.Language
	}
	locale, _, localeErr := i18n.ResolveLocale(i18n.LocaleInputs{Flag: flag(argv, "--lang"), Env: os.Getenv("DO_SOMETHING_LANG"), Pin: pin})
	tr, e := i18n.New(locale)
	if e != nil {
		fmt.Fprintln(errout, e)
		return 1
	}
	if localeErr != nil {
		return fail(invalid(&argumentError{"lang", flag(argv, "--lang"), constraints["lang"], localeErr}), flag(argv, "--format"), tr, errout, nil)
	}
	a := Args{}
	parser, e := kong.New(&a, append(parameterOptions(), kong.Name("do-something"), kong.Writers(out, errout), kong.NoDefaultHelp())...)
	if e != nil {
		return fail(e, flag(argv, "--format"), tr, errout, nil)
	}
	// Locate the command page for --help: the command is the first token that
	// is neither a help flag nor a global flag (value-taking global flags also
	// consume their value, so a value can never masquerade as a command).
	valueFlags := map[string]bool{}
	for _, f := range parser.Model.Node.Flags {
		if !f.IsBool() {
			valueFlags[f.Name] = true
		}
	}
	node := parser.Model.Node
	seenCommand := false
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		if v == "--help" || v == "-h" {
			help(parser, node, tr, out)
			return 0
		}
		if !strings.HasPrefix(v, "--") {
			if !seenCommand {
				// First non-flag token: the command, if any. Later non-flag
				// tokens are command arguments and never start a new page.
				for _, child := range parser.Model.Children {
					if child.Name == v {
						node = child
					}
				}
				seenCommand = true
			}
			continue
		}
		if !seenCommand && valueFlags[strings.TrimPrefix(v, "--")] && !strings.Contains(v, "=") {
			i++ // skip the flag's value
		}
	}
	ctx, e := parser.Parse(argv)
	if e != nil {
		return fail(invalid(e), flag(argv, "--format"), tr, errout, nil)
	}
	device, e := config.LoadDevice(config.DeviceOptions{DBPath: a.DB, SyncDir: a.Syncdir, Color: a.Color, Language: a.Lang}, nil)
	if e != nil {
		return fail(invalid(e), a.Format, tr, errout, nil)
	}
	locale, _, e = i18n.ResolveLocale(i18n.LocaleInputs{Flag: a.Lang, Env: os.Getenv("DO_SOMETHING_LANG"), Pin: device.Language})
	if e != nil {
		return fail(invalid(e), a.Format, tr, errout, nil)
	}
	tr, e = i18n.New(locale)
	if e != nil {
		return fail(e, a.Format, tr, errout, nil)
	}
	command := strings.Fields(ctx.Command())[0]
	interactive := !a.NoInput && (isatty.IsTerminal(in.Fd()) || isatty.IsCygwinTerminal(in.Fd()))
	emit := func(v any) error {
		if a.Format == "json" {
			return output.JSON(out, v)
		}
		return output.RenderValue(out, v, tr)
	}
	switch command {
	case "version":
		if a.Format == "json" {
			e = output.JSON(out, output.VersionResult{SchemaVersion: output.SchemaVersion, Version: Version})
		} else {
			_, e = fmt.Fprintln(out, "do-something "+Version)
		}
		if e != nil {
			return fail(e, a.Format, tr, errout, nil)
		}
		return 0
	case "schema":
		e = output.JSON(out, schema(parser))
		if e != nil {
			return fail(e, a.Format, tr, errout, nil)
		}
		return 0
	case "completion":
		completion(parser, a.Completion.Shell, out)
		return 0
	case "help":
		if e := helpTopic(parser, a.Help.Topic, tr, out); e != nil {
			return fail(invalid(e), a.Format, tr, errout, nil)
		}
		return 0
	}
	device.DBPath, e = paths.Canonical(device.DBPath)
	if e != nil {
		return fail(e, a.Format, tr, errout, nil)
	}
	if device.SyncDir != "" {
		device.SyncDir, e = paths.Canonical(device.SyncDir)
		if e != nil {
			return fail(e, a.Format, tr, errout, nil)
		}
	}
	opts := store.Options{DBPath: device.DBPath, StateDir: device.DBPath + ".state"}
	if device.SyncDir != "" {
		dbabs, _ := filepath.Abs(device.DBPath)
		syncabs, _ := filepath.Abs(device.SyncDir)
		rel, _ := filepath.Rel(syncabs, dbabs)
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fail(invalid(fmt.Errorf("working database must be outside syncdir")), a.Format, tr, errout, nil)
		}
	}
	writing := command == "log" || command == "add" || command == "edit" || command == "start" || command == "done" || command == "drop" || command == "reopen" || command == "deps" && a.Deps.Action != "list" || command == "config" && (a.Config.Action == "set" || a.Config.Action == "unset") || command == "import" && !a.Import.DryRun || command == "sync" && !a.Sync.DryRun || command == "snapshot"
	// Validate incomplete input before creating a store.
	if command == "add" && (a.Add.Title == "" || a.Add.Kind == "") && !interactive {
		return fail(invalid(fmt.Errorf("add requires --title and --kind when input is disabled")), a.Format, tr, errout, nil)
	}
	if command == "log" && a.Log.Duration != nil && *a.Log.Duration <= 0 {
		return fail(&store.LogError{Reason: "duration_invalid"}, a.Format, tr, errout, nil)
	}
	if writing {
		unlock, e := store.Acquire(opts)
		if e != nil {
			return fail(e, a.Format, tr, errout, nil)
		}
		defer unlock()
		opts.Locked = true
	}
	if command == "sync" {
		count := 0
		for _, v := range []bool{a.Sync.KeepLocal, a.Sync.TakeRemote, a.Sync.Merge} {
			if v {
				count++
			}
		}
		if count > 1 || a.Sync.Master != "" && !a.Sync.Merge || a.Sync.Master != "" && a.Sync.Master != "local" && a.Sync.Master != "remote" {
			return fail(invalid(fmt.Errorf("choose one sync resolution; master requires merge")), a.Format, tr, errout, nil)
		}
		if _, statErr := os.Stat(device.DBPath); os.IsNotExist(statErr) {
			if !a.Sync.TakeRemote {
				return fail(invalid(fmt.Errorf("a missing store requires sync --take-remote")), a.Format, tr, errout, nil)
			}
			tmp, err := os.MkdirTemp("", "do-something-bootstrap-")
			if err != nil {
				return fail(err, a.Format, tr, errout, nil)
			}
			defer os.RemoveAll(tmp)
			captured := filepath.Join(tmp, "remote.db")
			if err = store.CaptureSnapshot(a.Sync.File, captured); err != nil {
				return fail(err, a.Format, tr, errout, nil)
			}
			c, _, e := store.ReadSnapshot(captured)
			if e != nil {
				return fail(e, a.Format, tr, errout, nil)
			}
			digest, _ := c.Digest(true)
			rep := syncer.Report{SchemaVersion: output.SchemaVersion, Decision: "bootstrap", Changed: !a.Sync.DryRun, DryRun: a.Sync.DryRun, Baseline: digest, Conflicts: []syncer.Conflict{}, CorruptEvents: []string{}, Archives: []string{}}
			if !a.Sync.DryRun {
				if e = store.BootstrapFrom(opts.StateDir, opts.DBPath, captured); e != nil {
					return fail(e, a.Format, tr, errout, nil)
				}
				s, e := store.Open(store.OpenWrite, opts)
				if e != nil {
					return fail(e, a.Format, tr, errout, nil)
				}
				defer s.Close()
				peer := strings.TrimSuffix(filepath.Base(a.Sync.File), filepath.Ext(a.Sync.File))
				if e = s.SavePeerState(peer, digest, captured); e != nil {
					return fail(e, a.Format, tr, errout, nil)
				}
				path, _, e := s.HistoryArchive(digest, peer, captured)
				if e != nil {
					return fail(e, a.Format, tr, errout, nil)
				}
				rep.Archives = append(rep.Archives, path)
				if e = publish(s, device); e != nil {
					return fail(e, a.Format, tr, errout, nil)
				}
			}
			if e = emit(rep); e != nil {
				return fail(e, a.Format, tr, errout, nil)
			}
			return 0
		}
	}
	// A dry-run import uses a disposable store if there is no working store yet.
	if command == "import" && a.Import.DryRun {
		if _, err := os.Stat(opts.DBPath); os.IsNotExist(err) {
			dir, err := os.MkdirTemp("", "do-something-import-")
			if err != nil {
				return fail(err, a.Format, tr, errout, nil)
			}
			defer os.RemoveAll(dir)
			opts = store.Options{DBPath: filepath.Join(dir, "list.db")}
			temp, err := store.Open(store.OpenWrite, opts)
			if err != nil {
				return fail(err, a.Format, tr, errout, nil)
			}
			temp.Close()
		}
	}
	mode := store.OpenRead
	if writing {
		mode = store.OpenWrite
	}
	s, e := store.Open(mode, opts)
	if e != nil {
		if command == "doctor" {
			report := doctorReport{SchemaVersion: output.SchemaVersion, Healthy: false, StorePath: opts.DBPath, Problems: []string{e.Error()}, Checks: map[string]string{"openability": e.Error(), "quick_check": "not_run", "foreign_key_check": "not_run", "schema": "not_run"}, DeviceConfig: device, Locale: tr.Locale(), Locales: i18n.KnownLocales}
			return fail(e, a.Format, tr, errout, report)
		}
		return fail(e, a.Format, tr, errout, nil)
	}
	defer s.Close()
	c, e := effectiveConfig(s)
	if e != nil {
		return fail(e, a.Format, tr, errout, nil)
	}
	if command != "sync" && command != "doctor" {
		advisory(s, device, tr, errout)
	}
	changed := false
	var result any
	switch command {
	case "suggest", "list", "show":
		f := a.Suggest
		browse := command != "suggest"
		id := ""
		if command == "list" {
			f = a.List
		}
		if command == "show" {
			id, e = s.ResolveItemRef(a.Show.ID)
			f.Explain = true
			f.IncludeUnready = true
		}
		if e != nil {
			break
		}
		if command == "suggest" && f.Kind == "" {
			f.Kind = string(model.KindProject)
		}
		var r output.Result
		r, e = query(s, c, f, browse, id)
		if e != nil {
			break
		}
		if !browse && !f.Peek {
			unlock, le := store.Acquire(opts)
			if le != nil {
				e = le
				break
			}
			writeOpts := opts
			writeOpts.Locked = true
			writer, we := store.Open(store.OpenWrite, writeOpts)
			if we == nil {
				we = record(writer, &r)
				if we == nil {
					we = publish(writer, device)
				}
				writer.Close()
			}
			unlock()
			if we != nil {
				e = we
				break
			}
		}
		if a.Format == "json" {
			e = output.JSON(out, r)
		} else {
			tty := false
			if f, ok := out.(*os.File); ok {
				tty = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
			}
			color := tty && device.Color != "never"
			e = output.Text(out, r, tr, color)
		}
		if e != nil {
			break
		}
		if f.Explain && interactive && a.Format != "json" && len(r.Items) > 0 {
			if e = refreshRatings(r.Items[0].ID, s, opts, device, in, errout, tr); e != nil {
				return fail(e, a.Format, tr, errout, nil)
			}
		}
		if f.FailEmpty && r.Matched == 0 {
			return 3
		}
		return 0
	case "add", "edit":
		f := a.Add
		id := ""
		var before *model.Item
		if command == "edit" {
			f = a.Edit.Entry
			id, e = s.ResolveItemRef(a.Edit.ID)
			if e != nil {
				break
			}
			before, e = s.GetItem(id)
			if e != nil {
				break
			}
		}
		updates := map[string]string{}
		if (command == "add" && (f.Title == "" || f.Kind == "")) || (command == "edit" && reflect.DeepEqual(f, Entry{})) {
			if !interactive {
				e = invalid(fmt.Errorf("provide entry flags or use an interactive terminal"))
				break
			}
			updates, e = promptEntry(&f, before, c, in, errout, tr)
			if e != nil {
				break
			}
		}
		var spec store.ItemSpec
		spec, e = entrySpec(f, s, id)
		if e != nil {
			break
		}
		if command == "add" && spec.MinSessionDuration == nil && !spec.ClearMinSessionDuration && spec.Type != nil {
			if v, ok := c.MinSessionDurationDefault(*spec.Type); ok {
				spec.MinSessionDuration = &v
			}
		}
		id, e = s.Save(spec, command == "add")
		if e != nil {
			e = invalid(e)
			break
		}
		after, ge := s.GetItem(id)
		if ge != nil {
			e = ge
			break
		}
		changed = !reflect.DeepEqual(before, after)
		for k, v := range updates {
			if e = s.SetConfig(k, v); e != nil {
				break
			}
		}
		result = output.ItemMutation{SchemaVersion: output.SchemaVersion, Changed: changed, ID: id, Before: before, After: after}
	case "log":
		var r output.LogMutation
		r, e = logActivity(s, a.Log, interactive, in, errout, tr)
		changed = r.Changed
		result = r
	case "start", "done", "drop", "reopen":
		ids := map[string][]string{"start": a.Start.IDs, "done": a.Done.IDs, "drop": a.Drop.IDs, "reopen": a.Reopen.IDs}[command]
		verb, _ := store.TransitionOf(command)
		ids, e = resolveTransitionRefs(s, verb, ids)
		if e != nil {
			break
		}
		r, err := s.TransitionStatus(verb, ids)
		e = err
		for _, v := range r {
			changed = changed || v.Changed
		}
		allIDs, _, refsErr := allItemReferences(s)
		if e == nil {
			e = refsErr
		}
		result = output.StatusMutation{SchemaVersion: output.SchemaVersion, Changed: changed, Items: r, AllIDs: allIDs}
	case "deps":
		id, err := s.ResolveItemRef(a.Deps.ID)
		e = err
		if e != nil {
			break
		}
		deps, err := s.Dependencies(id)
		e = err
		if e != nil {
			break
		}
		before := append([]string{}, deps...)
		if deps == nil {
			deps = []string{}
		}
		if a.Deps.Action != "list" {
			if len(a.Deps.IDs) == 0 {
				e = invalid(fmt.Errorf("dependency references required"))
				break
			}
			for _, p := range a.Deps.IDs {
				dep, err := s.ResolveItemRef(p)
				if err != nil {
					e = err
					break
				}
				if a.Deps.Action == "add" {
					exists := false
					for _, d := range deps {
						exists = exists || d == dep
					}
					if !exists {
						deps = append(deps, dep)
					}
				} else {
					next := []string{}
					for _, d := range deps {
						if d != dep {
							next = append(next, d)
						}
					}
					deps = next
				}
			}
			if e != nil {
				break
			}
			changed = !reflect.DeepEqual(before, deps)
			if changed {
				_, e = s.Save(store.ItemSpec{ID: id, DependsOn: &deps}, false)
			}
		}
		allIDs, refs, refsErr := allItemReferences(s)
		if e == nil {
			e = refsErr
		}
		toRefs := func(ids []string) []output.ItemReference {
			out := make([]output.ItemReference, 0, len(ids))
			for _, id := range ids {
				out = append(out, refs[id])
			}
			return out
		}
		result = output.Dependencies{SchemaVersion: output.SchemaVersion, Changed: changed, ID: id, Title: refs[id].Title, Before: before, After: deps, Dependencies: deps, BeforeItems: toRefs(before), AfterItems: toRefs(deps), DependencyItems: toRefs(deps), AllIDs: allIDs}
	case "config":
		if a.Config.Action != "" && a.Config.Action != "get" && a.Config.Action != "set" && a.Config.Action != "unset" {
			e = invalid(fmt.Errorf("config action must be get, set, or unset"))
			break
		}
		if a.Config.Action != "set" && a.Config.Value != nil {
			e = invalid(fmt.Errorf("only config set accepts a value"))
			break
		}
		if a.Config.Action != "" && a.Config.Key == "" {
			e = invalid(fmt.Errorf("config key is required"))
			break
		}
		switch a.Config.Action {
		case "set":
			if a.Config.Value == nil {
				e = invalid(fmt.Errorf("config value is required"))
				break
			}
			if e = config.ValidateValue(a.Config.Key, *a.Config.Value); e != nil {
				e = invalid(e)
				break
			}
			old, ok, err := s.GetConfig(a.Config.Key)
			e = err
			if e != nil {
				break
			}
			changed = !ok || old != *a.Config.Value
			if changed {
				e = s.SetConfig(a.Config.Key, *a.Config.Value)
			}
		case "unset":
			changed, e = s.UnsetConfig(a.Config.Key)
		}
		if e != nil {
			break
		}
		c, e = effectiveConfig(s)
		if e != nil {
			break
		}
		values := map[string]output.ConfigValue{}
		for _, k := range c.Keys() {
			if a.Config.Key == "" || a.Config.Key == k {
				values[k] = output.ConfigValue{Value: c.StringValue(k), Source: c.Source(k)}
			}
		}
		result = output.ConfigResult{SchemaVersion: output.SchemaVersion, Changed: changed, Config: values}
	case "export":
		var content store.Content
		content, e = s.Content()
		if e != nil {
			break
		}
		e = output.JSON(out, content)
		if e == nil {
			return 0
		}
	case "import":
		var data []byte
		if a.Import.File == "-" {
			data, e = io.ReadAll(in)
		} else {
			data, e = os.ReadFile(a.Import.File)
		}
		if e != nil {
			break
		}
		local, err := s.Content()
		if err != nil {
			e = err
			break
		}
		incoming, err := store.DecodeContent(data)
		if err != nil {
			e = invalid(err)
			break
		}
		merged, rep, err := importp.Upsert(local, incoming)
		e = err
		if e != nil {
			break
		}
		rep.DryRun = a.Import.DryRun
		changed = rep.Changed && !a.Import.DryRun
		if changed {
			e = s.ApplyContent(merged)
		}
		result = rep
	case "snapshot":
		target := a.Snapshot.Target
		if target == "" {
			target = filepath.Join(filepath.Dir(device.DBPath), "snapshot-"+time.Now().UTC().Format("20060102T150405.000000000")+".db")
		}
		abs, _ := filepath.Abs(target)
		dbabs, _ := filepath.Abs(device.DBPath)
		if abs == dbabs {
			e = invalid(fmt.Errorf("snapshot target must differ from working database"))
			break
		}
		e = s.VacuumInto(target)
		result = output.SnapshotResult{SchemaVersion: output.SchemaVersion, Path: target}
	case "sync":
		resolution := ""
		if a.Sync.KeepLocal {
			resolution = "keep-local"
		}
		if a.Sync.TakeRemote {
			resolution = "take-remote"
		}
		if a.Sync.Merge {
			resolution = "merge"
		}
		o := syncer.Options{Resolution: resolution, Master: a.Sync.Master, DryRun: a.Sync.DryRun}
		var rep syncer.Report
		rep, e = syncer.Run(s, a.Sync.File, o)

		reader := bufio.NewReader(in)
		for errors.Is(e, syncer.ErrConflict) && interactive && !o.DryRun {
			if len(rep.Conflicts) > 0 && o.Resolution == "merge" {
				o.Choices = map[string]string{}
				for _, conflict := range rep.Conflicts {
					if err := output.RenderValue(errout, conflict, tr); err != nil {
						e = err
						break
					}
					fmt.Fprintln(errout, tr.T("prompt.conflict"))
					answer, err := reader.ReadString('\n')
					if err != nil {
						e = err
						break
					}
					choice := strings.TrimSpace(answer)
					if choice != "local" && choice != "remote" {
						e = invalid(fmt.Errorf("choose local or remote"))
						break
					}
					o.Choices[conflict.Table+"/"+conflict.Key] = choice
				}
				if !errors.Is(e, syncer.ErrConflict) {
					break
				}
			} else {
				if o.Resolution != "" {
					break
				} // An invalid combined graph needs an explicit new command.
				fmt.Fprintln(errout, tr.T("prompt.sync"))
				answer, err := reader.ReadString('\n')
				if err != nil {
					e = err
					break
				}
				o.Resolution = strings.TrimSpace(answer)
				if o.Resolution != "keep-local" && o.Resolution != "take-remote" && o.Resolution != "merge" {
					e = invalid(fmt.Errorf("choose keep-local, take-remote, or merge"))
					break
				}
			}
			rep, e = syncer.Run(s, a.Sync.File, o)
		}

		if e != nil {
			return fail(e, a.Format, tr, errout, rep)
		}
		changed = rep.Changed && !rep.DryRun
		result = rep
	case "doctor":
		result, e = doctor(s, device, tr)
	}
	if e != nil {
		return fail(e, a.Format, tr, errout, result)
	}
	if changed || command == "sync" && !a.Sync.DryRun {
		if e = publish(s, device); e != nil {
			return fail(e, a.Format, tr, errout, result)
		}
	}
	if result != nil {
		if a.Format == "json" {
			e = emit(result)
		} else {
			switch value := result.(type) {
			case output.LogMutation:
				e = output.LogText(out, value, tr)
			case output.StatusMutation:
				e = output.StatusText(out, value, tr)
			case output.Dependencies:
				e = output.DependenciesText(out, value, tr)
			default:
				e = output.RenderValue(out, result, tr)
			}
		}
	}
	if e != nil {
		return fail(e, a.Format, tr, errout, result)
	}
	return 0
}

func allItemReferences(s *store.Store) ([]string, map[string]output.ItemReference, error) {
	items, err := s.AllItems()
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(items))
	refs := make(map[string]output.ItemReference, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		refs[item.ID] = output.ItemReference{ID: item.ID, Title: item.Title}
	}
	return ids, refs, nil
}

func publish(s *store.Store, d *config.DeviceConfig) error {
	if d.SyncDir == "" {
		return nil
	}
	id, e := s.DeviceID()
	if e != nil {
		return e
	}
	_, e = s.Publish(d.SyncDir, id, d.DeviceName)
	return e
}
func fail(e error, format string, tr *i18n.Translator, w io.Writer, details any) int {
	code := "IO_ERROR"
	exit := 1
	var inv invocationError
	if errors.As(e, &inv) {
		code = "INVALID_ARGUMENT"
		exit = 2
	}
	for _, s := range []error{store.ErrNoStore, store.ErrStoreCorrupt, store.ErrStoreLocked, store.ErrSchemaMismatch, store.ErrInvalidID, store.ErrCycle, store.ErrNoPeerBase, syncer.ErrConflict, syncer.ErrIdentity} {
		if errors.Is(e, s) {
			code = s.Error()
		}
	}
	var ambiguous *store.AmbiguousIDError
	if errors.As(e, &ambiguous) {
		details = ambiguous
	}
	var ambiguousTitle *store.AmbiguousTitleError
	if errors.As(e, &ambiguousTitle) {
		details = ambiguousTitle
	}
	var te *store.TransitionError
	if errors.As(e, &te) {
		code = "INVALID_ID"
		details = te
	}
	var logErr *store.LogError
	if errors.As(e, &logErr) {
		code = "INVALID_STATE"
		if logErr.Reason == "duration_invalid" {
			code = "INVALID_ARGUMENT"
		}
		exit = 2
	}
	if code == "INVALID_ID" || code == "CYCLE" {
		exit = 2
	}
	if format == "json" {
		body := output.ErrorBody{Code: code, Message: e.Error(), Details: details}
		var arg *argumentError
		if errors.As(e, &arg) {
			body.Argument = arg.Argument
			body.Value = arg.Value
			body.Allowed = arg.Constraint.Allowed
			body.Minimum = arg.Constraint.Minimum
			body.ExclusiveMinimum = arg.Constraint.ExclusiveMinimum
			body.Maximum = arg.Constraint.Maximum
			body.Format = arg.Constraint.Format
		}
		_ = output.JSON(w, output.ErrorResult{SchemaVersion: output.SchemaVersion, Error: body})
	} else {
		detail := e.Error()
		if tr.Locale() != "en" {
			detail = tr.T("error.code." + strings.ToLower(code))
		}
		var arg *argumentError
		if errors.As(e, &arg) {
			detail = tr.T("error.argument", "argument", arg.Argument, "value", fmt.Sprint(arg.Value))
			if len(arg.Constraint.Allowed) > 0 {
				detail += "\n  " + tr.T("error.allowed", "values", strings.Join(arg.Constraint.Allowed, ", "))
			}
		}
		var ongoingErr *model.OngoingDurationError
		if errors.As(e, &ongoingErr) {
			detail = tr.T("error.ongoing_duration")
		}
		if logErr != nil {
			detail = tr.T("error.log." + logErr.Reason)
		}
		fmt.Fprintln(w, tr.T("error.generic", "code", code, "detail", detail))
	}
	return exit
}
